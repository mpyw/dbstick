package gormstick_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"

	"github.com/mpyw/dbstick/gormstick"
)

// stickyKey marks a sticky context.
type stickyKey struct{}

// checker reports a context as sticky if it has stickyKey.
type checker struct{}

var _ gormstick.Checker = checker{}

func (checker) IsSticky(ctx context.Context) bool {
	return ctx.Value(stickyKey{}) != nil
}

var sticky = context.WithValue(context.Background(), stickyKey{}, true)

type user struct {
	ID   int
	Name string
}

// openSQLite opens a SQLite file whose users table holds one user, named
// name. So a read shows which file it went to.
func openSQLite(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := sql.Open(sqlite.DriverName, filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() }) // the test is over
	for _, q := range []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)",
		"INSERT INTO users VALUES (1, '" + name + "')",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// newDB returns a GORM database whose source and replica are two SQLite
// files, with the plugin.
func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Dialector{Conn: openSQLite(t, "source")}, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	err = db.Use(dbresolver.Register(dbresolver.Config{
		Replicas: []gorm.Dialector{sqlite.Dialector{Conn: openSQLite(t, "replica")}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Use(gormstick.New(checker{})); err != nil {
		t.Fatal(err)
	}
	return db
}

// wantReads checks that each way of reading in ctx gets the user of want.
func wantReads(t *testing.T, db *gorm.DB, ctx context.Context, want string) {
	t.Helper()
	db = db.WithContext(ctx)

	var u user
	if err := db.First(&u, 1).Error; err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.Raw("SELECT name FROM users WHERE id = 1").Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	var row string
	if err := db.Table("users").Select("name").Where("id = 1").Row().Scan(&row); err != nil {
		t.Fatal(err)
	}
	for way, got := range map[string]string{"First": u.Name, "Raw": raw, "Row": row} {
		if got != want {
			t.Errorf("%s read from %q, want %q", way, got, want)
		}
	}
}

func TestNotSticky(t *testing.T) {
	wantReads(t, newDB(t), context.Background(), "replica")
}

func TestSticky(t *testing.T) {
	wantReads(t, newDB(t), sticky, "source")
}

// In a transaction, dbresolver keeps the transaction's connection, sticky
// or not.
func TestTransaction(t *testing.T) {
	db := newDB(t)
	for _, ctx := range []context.Context{context.Background(), sticky} {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var u user
			if err := tx.First(&u, 1).Error; err != nil {
				return err
			}
			if u.Name != "source" {
				t.Errorf("read in a transaction from %q, want %q", u.Name, "source")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewPanicsOnNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("did not panic")
		}
	}()
	gormstick.New(nil)
}

func TestNeedsDBResolver(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Use(gormstick.New(checker{})); err == nil {
		t.Error("Use without dbresolver: no error")
	}
}

// With Sources, dbresolver writes to them, and a sticky read goes there
// too, not to the database that gorm.Open opened.
func TestSources(t *testing.T) {
	db, err := gorm.Open(sqlite.Dialector{Conn: openSQLite(t, "main")}, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	err = db.Use(dbresolver.Register(dbresolver.Config{
		Sources:  []gorm.Dialector{sqlite.Dialector{Conn: openSQLite(t, "source")}},
		Replicas: []gorm.Dialector{sqlite.Dialector{Conn: openSQLite(t, "replica")}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Use(gormstick.New(checker{})); err != nil {
		t.Fatal(err)
	}
	wantReads(t, db, context.Background(), "replica")
	wantReads(t, db, sticky, "source")
}

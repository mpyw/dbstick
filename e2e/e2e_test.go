// Package e2e tests dbstick with its hooks, the way an app uses them. It
// is a module of its own that is never published, so it may use replace
// directives to build with the modules in this repository.
package e2e_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"

	"github.com/mpyw/dbstick"
	"github.com/mpyw/dbstick/gormstick"
	"github.com/mpyw/dbstick/pgxstick"
	"github.com/mpyw/dbstick/redisstore"
	"github.com/mpyw/dbstick/sqlstick"
)

// The hooks do not import dbstick. These keep their interfaces and the
// Tracker in step.
var (
	_ pgxstick.Recorder = (*dbstick.Tracker)(nil)
	_ sqlstick.Recorder = (*dbstick.Tracker)(nil)
	_ gormstick.Checker = (*dbstick.Tracker)(nil)
	_ dbstick.Store     = (*redisstore.Store)(nil)
)

type userKey struct{}

func userID(ctx context.Context) string {
	id, _ := ctx.Value(userKey{}).(string) // "" for a guest
	return id
}

type user struct {
	ID   int
	Name string
}

// app is a GORM database whose source and replica are two SQLite files,
// each holding a user of its own name. The source records its writes
// through sqlstick.
type app struct {
	tracker *dbstick.Tracker
	db      *gorm.DB
}

func newApp(t *testing.T) *app {
	t.Helper()
	dir := t.TempDir()
	tracker := dbstick.NewTracker(dbstick.NewMemoryStore(), userID)

	plain, err := sql.Open(sqlite.DriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	d := plain.Driver()
	_ = plain.Close() // only its driver is used

	// open opens a file, through sqlstick if recorder is not nil.
	open := func(name string, recorder sqlstick.Recorder) *sql.DB {
		var db *sql.DB
		if recorder != nil {
			connector, err := sqlstick.WrapDriver(d, filepath.Join(dir, name), recorder)
			if err != nil {
				t.Fatal(err)
			}
			db = sql.OpenDB(connector)
		} else {
			var err error
			if db, err = sql.Open(sqlite.DriverName, filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
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
	source := open("source", tracker)
	replica := open("replica", nil)

	db, err := gorm.Open(sqlite.Dialector{Conn: source}, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	err = db.Use(dbresolver.Register(dbresolver.Config{
		Replicas: []gorm.Dialector{sqlite.Dialector{Conn: replica}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Use(gormstick.New(tracker)); err != nil {
		t.Fatal(err)
	}
	return &app{tracker: tracker, db: db}
}

// request returns the context of a new request of alice.
func (a *app) request() context.Context {
	return context.WithValue(a.tracker.NewContext(context.Background()), userKey{}, "alice")
}

func (a *app) name(t *testing.T, ctx context.Context) string {
	t.Helper()
	var u user
	if err := a.db.WithContext(ctx).First(&u, 1).Error; err != nil {
		t.Fatal(err)
	}
	return u.Name
}

func TestReadYourWrites(t *testing.T) {
	a := newApp(t)
	if got := a.name(t, a.request()); got != "replica" {
		t.Errorf("read before a write from %q, want the replica", got)
	}

	ctx := a.request()
	if err := a.db.WithContext(ctx).Model(&user{ID: 1}).Update("name", "written").Error; err != nil {
		t.Fatal(err)
	}
	if got := a.name(t, ctx); got != "written" {
		t.Errorf("read in the request from %q, want the source", got)
	}
	if got := a.name(t, a.request()); got != "written" {
		t.Errorf("read in the next request from %q, want the source", got)
	}

	other := context.WithValue(a.tracker.NewContext(context.Background()), userKey{}, "bob")
	if got := a.name(t, other); got != "replica" {
		t.Errorf("read of another user from %q, want the replica", got)
	}
}

func TestTransaction(t *testing.T) {
	a := newApp(t)
	ctx := a.request()
	err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&user{ID: 1}).Update("name", "in tx").Error; err != nil {
			return err
		}
		if got := a.name(t, a.request()); got != "replica" {
			t.Errorf("read in the next request before the commit from %q, want the replica", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.name(t, a.request()); got != "in tx" {
		t.Errorf("read in the next request after the commit from %q, want the source", got)
	}
}

// TestPgx runs against DBSTICK_POSTGRES_URL. The replica is the same
// server, since the test only checks which handle each read gets.
func TestPgx(t *testing.T) {
	url := os.Getenv("DBSTICK_POSTGRES_URL")
	if url == "" {
		t.Skip("DBSTICK_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	tracker := dbstick.NewTracker(dbstick.NewMemoryStore(), userID)

	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Tracer = pgxstick.NewTracer(tracker)
	primary, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(primary.Close)
	replica, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(replica.Close)
	db := dbstick.NewRouter(tracker, primary, replica)

	table := fmt.Sprintf("e2e_%d", os.Getpid())
	if _, err := replica.Exec(ctx, "CREATE TABLE "+table+" (id int)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = replica.Exec(ctx, "DROP TABLE "+table) // the test is over
	})

	request := func(id string) context.Context {
		return context.WithValue(tracker.NewContext(ctx), userKey{}, id)
	}
	readsPrimary := func(ctx context.Context) bool {
		return db.ForRead(ctx) == primary
	}

	alice := request("alice")
	err = pgx.BeginFunc(alice, db.ForWrite(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(alice, "INSERT INTO "+table+" VALUES (1)"); err != nil {
			return err
		}
		if !readsPrimary(alice) {
			t.Error("the request reads from the replica before the commit")
		}
		if readsPrimary(request("alice")) {
			t.Error("the next request reads from the primary before the commit")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !readsPrimary(request("alice")) {
		t.Error("the next request reads from the replica after the commit")
	}
	if readsPrimary(request("bob")) {
		t.Error("another user reads from the primary")
	}
}

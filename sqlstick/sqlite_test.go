package sqlstick_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"modernc.org/sqlite"

	"github.com/mpyw/dbstick/sqlstick"
)

// requestKey marks the context of the request under test.
type requestKey struct{}

// recorder keeps the names of the calls made with the request's
// context.
type recorder struct {
	calls []string
}

var _ sqlstick.Recorder = (*recorder)(nil)

func (r *recorder) add(ctx context.Context, name string) {
	if ctx.Value(requestKey{}) != nil {
		r.calls = append(r.calls, name)
	}
}

func (r *recorder) MarkWritten(ctx context.Context)     { r.add(ctx, "MarkWritten") }
func (r *recorder) MarkUncommitted(ctx context.Context) { r.add(ctx, "MarkUncommitted") }
func (r *recorder) MarkCommitted(ctx context.Context)   { r.add(ctx, "MarkCommitted") }
func (r *recorder) MarkRolledBack(ctx context.Context)  { r.add(ctx, "MarkRolledBack") }

// env is a recorder and a wrapped SQLite database with a table t.
type env struct {
	rec *recorder
	db  *sql.DB
	// ctx is the request's context.
	ctx context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	rec := &recorder{}
	connector, err := sqlstick.WrapDriver(&sqlite.Driver{}, filepath.Join(t.TempDir(), "db"), rec)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() }) // the test is over
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	return &env{
		rec: rec,
		db:  db,
		ctx: context.WithValue(context.Background(), requestKey{}, true),
	}
}

func (e *env) wantCalls(t *testing.T, want ...string) {
	t.Helper()
	if !slices.Equal(e.rec.calls, want) {
		t.Errorf("calls = %v, want %v", e.rec.calls, want)
	}
}

func TestSQLiteExec(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup string
		query string
		want  []string
	}{
		{"INSERT", "", "INSERT INTO t VALUES (1)", []string{"MarkWritten"}},
		{"UPDATE", "INSERT INTO t VALUES (1)", "UPDATE t SET id = 2", []string{"MarkWritten"}},
		{"UPDATE of no rows", "", "UPDATE t SET id = 2", nil},
		{"DDL", "", "CREATE TABLE u (id INTEGER)", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if tc.setup != "" {
				if _, err := e.db.Exec(tc.setup); err != nil { // outside any scope
					t.Fatal(err)
				}
			}
			if _, err := e.db.ExecContext(e.ctx, tc.query); err != nil {
				t.Fatal(err)
			}
			e.wantCalls(t, tc.want...)
		})
	}
}

func TestSQLiteQuery(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"INSERT INTO t VALUES (1) RETURNING id", []string{"MarkWritten"}},
		{"SELECT 1", nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			e := newEnv(t)
			var id int
			if err := e.db.QueryRowContext(e.ctx, tc.query).Scan(&id); err != nil {
				t.Fatal(err)
			}
			e.wantCalls(t, tc.want...)
		})
	}
}

func TestSQLitePrepared(t *testing.T) {
	e := newEnv(t)
	stmt, err := e.db.PrepareContext(e.ctx, "INSERT INTO t VALUES (?) RETURNING id")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stmt.Close() }) // the test is over
	var id int
	if err := stmt.QueryRowContext(e.ctx, 1).Scan(&id); err != nil {
		t.Fatal(err)
	}
	e.wantCalls(t, "MarkWritten")
}

func TestSQLiteTransaction(t *testing.T) {
	e := newEnv(t)
	tx, err := e.db.BeginTx(e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(e.ctx, "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	e.wantCalls(t, "MarkUncommitted", "MarkCommitted")
}

func TestSQLiteRollback(t *testing.T) {
	e := newEnv(t)
	tx, err := e.db.BeginTx(e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(e.ctx, "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// A later write outside a transaction is not taken for one.
	if _, err := e.db.ExecContext(e.ctx, "INSERT INTO t VALUES (2)"); err != nil {
		t.Fatal(err)
	}
	e.wantCalls(t, "MarkUncommitted", "MarkRolledBack", "MarkWritten")
}

func TestSQLiteFailedStatement(t *testing.T) {
	e := newEnv(t)
	if _, err := e.db.ExecContext(e.ctx, "INSERT INTO missing VALUES (1)"); err == nil {
		t.Fatal("no error")
	}
	e.wantCalls(t)
}

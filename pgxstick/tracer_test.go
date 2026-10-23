package pgxstick_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/mpyw/dbstick/pgxstick"
)

// requestKey marks the context of the request under test. The recorder
// keeps only the calls with it, so setup outside the request is left out,
// and a call with the wrong context shows.
type requestKey struct{}

// recorder keeps the names of the calls made with the request's context.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

var _ pgxstick.Recorder = (*recorder)(nil)

func (r *recorder) add(ctx context.Context, name string) {
	if ctx.Value(requestKey{}) == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name)
}

func (r *recorder) MarkWritten(ctx context.Context)     { r.add(ctx, "MarkWritten") }
func (r *recorder) MarkUncommitted(ctx context.Context) { r.add(ctx, "MarkUncommitted") }
func (r *recorder) MarkCommitted(ctx context.Context)   { r.add(ctx, "MarkCommitted") }
func (r *recorder) MarkRolledBack(ctx context.Context)  { r.add(ctx, "MarkRolledBack") }

// env is a recorder, a pool with its Tracer, and a table of its own.
type env struct {
	rec   *recorder
	pool  *pgxpool.Pool
	table string
	// ctx is the request's context.
	ctx context.Context
}

var tableSeq atomic.Int64

// newEnv connects to DBSTICK_POSTGRES_URL, or skips the test.
func newEnv(t *testing.T) *env {
	t.Helper()
	url := os.Getenv("DBSTICK_POSTGRES_URL")
	if url == "" {
		t.Skip("DBSTICK_POSTGRES_URL is not set")
	}
	rec := &recorder{}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Tracer = pgxstick.NewTracer(rec)
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	e := &env{
		rec:   rec,
		pool:  pool,
		table: fmt.Sprintf("pgxstick_%d_%d", os.Getpid(), tableSeq.Add(1)),
		ctx:   context.WithValue(context.Background(), requestKey{}, true),
	}
	e.exec(t, context.Background(), "CREATE TABLE %s (id int PRIMARY KEY)")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP TABLE "+e.table) // the test is over
	})
	return e
}

// exec runs query, with %s replaced by the table.
func (e *env) exec(t *testing.T, ctx context.Context, query string) {
	t.Helper()
	if _, err := e.pool.Exec(ctx, e.sql(query)); err != nil {
		t.Fatal(err)
	}
}

// sql replaces %s in query by the table.
func (e *env) sql(query string) string {
	return strings.ReplaceAll(query, "%s", e.table)
}

func (e *env) wantCalls(t *testing.T, want ...string) {
	t.Helper()
	e.rec.mu.Lock()
	defer e.rec.mu.Unlock()
	if !slices.Equal(e.rec.calls, want) {
		t.Errorf("calls = %v, want %v", e.rec.calls, want)
	}
}

func TestNewTracerPanicsOnNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("did not panic")
		}
	}()
	pgxstick.NewTracer(nil)
}

func TestWrites(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup string
		query string
		want  []string
	}{
		{"INSERT", "", "INSERT INTO %s VALUES (1)", []string{"MarkWritten"}},
		{"INSERT of no rows", "", "INSERT INTO %s SELECT 1 WHERE false", nil},
		{"UPDATE", "INSERT INTO %s VALUES (1)", "UPDATE %s SET id = 2", []string{"MarkWritten"}},
		{"UPDATE of no rows", "", "UPDATE %s SET id = 2", nil},
		{"DELETE", "INSERT INTO %s VALUES (1)", "DELETE FROM %s", []string{"MarkWritten"}},
		{"MERGE", "", "MERGE INTO %s t USING (SELECT 1 AS id) s ON t.id = s.id WHEN NOT MATCHED THEN INSERT VALUES (s.id)", []string{"MarkWritten"}},
		{"SELECT", "INSERT INTO %s VALUES (1)", "SELECT * FROM %s", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if tc.setup != "" {
				e.exec(t, context.Background(), tc.setup) // not recorded
			}
			e.exec(t, e.ctx, tc.query)
			e.wantCalls(t, tc.want...)
		})
	}
}

func TestQueryReturning(t *testing.T) {
	e := newEnv(t)
	var id int
	if err := e.pool.QueryRow(e.ctx, e.sql("INSERT INTO %s VALUES (1) RETURNING id")).Scan(&id); err != nil {
		t.Fatal(err)
	}
	e.wantCalls(t, "MarkWritten")
}

func TestFailedStatement(t *testing.T) {
	e := newEnv(t)
	if _, err := e.pool.Exec(e.ctx, e.sql("INSERT INTO %s VALUES ('x')")); err == nil {
		t.Fatal("no error")
	}
	e.wantCalls(t)
}

func TestTransaction(t *testing.T) {
	e := newEnv(t)
	err := pgx.BeginFunc(e.ctx, e.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(e.ctx, e.sql("INSERT INTO %s VALUES (1)"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	e.wantCalls(t, "MarkUncommitted", "MarkCommitted")
}

func TestEmptyTransaction(t *testing.T) {
	e := newEnv(t)
	if err := pgx.BeginFunc(e.ctx, e.pool, func(pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	e.wantCalls(t, "MarkCommitted")
}

func TestRollback(t *testing.T) {
	e := newEnv(t)
	tx, err := e.pool.Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(e.ctx, e.sql("INSERT INTO %s VALUES (1)")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.wantCalls(t, "MarkUncommitted", "MarkRolledBack")
}

func TestCommitOfFailedTransaction(t *testing.T) {
	e := newEnv(t)
	tx, err := e.pool.Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(e.ctx, e.sql("INSERT INTO %s VALUES (1)")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(e.ctx, e.sql("INSERT INTO %s VALUES (1)")); err == nil {
		t.Fatal("no error for a duplicate key")
	}
	// PostgreSQL answers ROLLBACK to the COMMIT of a failed transaction.
	if err := tx.Commit(e.ctx); !errors.Is(err, pgx.ErrTxCommitRollback) {
		t.Fatalf("Commit = %v, want %v", err, pgx.ErrTxCommitRollback)
	}
	e.wantCalls(t, "MarkUncommitted", "MarkRolledBack")
}

func TestSavepointRollback(t *testing.T) {
	e := newEnv(t)
	err := pgx.BeginFunc(e.ctx, e.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(e.ctx, e.sql("INSERT INTO %s VALUES (1)")); err != nil {
			return err
		}
		// A nested transaction is a savepoint. Its ROLLBACK TO SAVEPOINT
		// leaves the outer transaction open, so it records nothing.
		nested, err := tx.Begin(e.ctx)
		if err != nil {
			return err
		}
		if _, err := nested.Exec(e.ctx, e.sql("INSERT INTO %s VALUES (2)")); err != nil {
			return err
		}
		return nested.Rollback(e.ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	e.wantCalls(t, "MarkUncommitted", "MarkUncommitted", "MarkCommitted")
}

func TestBatch(t *testing.T) {
	e := newEnv(t)
	b := &pgx.Batch{}
	b.Queue("SELECT 1")
	b.Queue(e.sql("INSERT INTO %s VALUES (1)"))
	if err := e.pool.SendBatch(e.ctx, b).Close(); err != nil {
		t.Fatal(err)
	}
	e.wantCalls(t, "MarkWritten")
}

func TestCopyFrom(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows [][]any
		want []string
	}{
		{"rows", [][]any{{1}, {2}}, []string{"MarkWritten"}},
		{"no rows", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			_, err := e.pool.CopyFrom(e.ctx, pgx.Identifier{e.table}, []string{"id"}, pgx.CopyFromRows(tc.rows))
			if err != nil {
				t.Fatal(err)
			}
			e.wantCalls(t, tc.want...)
		})
	}
}

func TestCopyFromInTransaction(t *testing.T) {
	e := newEnv(t)
	err := pgx.BeginFunc(e.ctx, e.pool, func(tx pgx.Tx) error {
		_, err := tx.CopyFrom(e.ctx, pgx.Identifier{e.table}, []string{"id"}, pgx.CopyFromRows([][]any{{1}}))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	e.wantCalls(t, "MarkUncommitted", "MarkCommitted")
}

// database/sql commits with no context. pgx's stdlib commits with the one
// of BeginTx, so the commit is recorded in the request.
func TestDatabaseSQL(t *testing.T) {
	e := newEnv(t)
	db := stdlib.OpenDBFromPool(e.pool)
	t.Cleanup(func() { _ = db.Close() }) // the test is over
	tx, err := db.BeginTx(e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(e.ctx, e.sql("INSERT INTO %s VALUES (1)")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	e.wantCalls(t, "MarkUncommitted", "MarkCommitted")
}

// Package pgxstick records the writes of pgx for a dbstick Tracker. It
// is a tracer: set it on the primary's pgx config, and every write that
// succeeds is recorded. It works for *pgx.Conn, *pgxpool.Pool, and
// *sql.DB through github.com/jackc/pgx/v5/stdlib.
//
// A write outside a transaction is recorded when it succeeds. A write in
// a transaction makes the scope sticky at once, and is put in the Store
// when the transaction commits. A rollback drops it.
package pgxstick

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Recorder records writes. *dbstick.Tracker of github.com/mpyw/dbstick
// is one. This package does not import dbstick, so its versions do not
// depend on dbstick's.
type Recorder interface {
	// MarkWritten records a write outside a transaction.
	MarkWritten(ctx context.Context)
	// MarkUncommitted records a write in a transaction.
	MarkUncommitted(ctx context.Context)
	// MarkCommitted records that a transaction committed.
	MarkCommitted(ctx context.Context)
	// MarkRolledBack records that a transaction rolled back.
	MarkRolledBack(ctx context.Context)
}

// Tracer is a pgx tracer that records writes. It implements
// pgx.QueryTracer, pgx.BatchTracer and pgx.CopyFromTracer. To use it with
// another tracer, such as one for OpenTelemetry, combine them with
// github.com/jackc/pgx/v5/multitracer.
//
// A write is a statement whose command tag is INSERT, UPDATE, DELETE or
// MERGE with at least one row, or a CopyFrom with at least one row. Any
// other write is not seen, such as a function that writes, called with
// SELECT. Call MarkWritten of the Recorder for those.
type Tracer struct {
	recorder Recorder
}

var (
	_ pgx.QueryTracer    = (*Tracer)(nil)
	_ pgx.BatchTracer    = (*Tracer)(nil)
	_ pgx.CopyFromTracer = (*Tracer)(nil)
)

// NewTracer returns a Tracer that records writes in recorder, such as a
// *dbstick.Tracker. It panics if recorder is nil.
func NewTracer(recorder Recorder) *Tracer {
	if recorder == nil {
		panic("pgxstick: NewTracer: recorder is nil")
	}
	return &Tracer{recorder: recorder}
}

// TraceQueryStart does nothing.
func (t *Tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

// TraceQueryEnd records a write, a commit or a rollback.
func (t *Tracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	t.record(ctx, conn.PgConn().TxStatus(), data.CommandTag, data.Err)
}

// TraceBatchStart does nothing.
func (t *Tracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return ctx
}

// TraceBatchQuery records a write, a commit or a rollback in a batch.
func (t *Tracer) TraceBatchQuery(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchQueryData) {
	t.record(ctx, conn.PgConn().TxStatus(), data.CommandTag, data.Err)
}

// TraceBatchEnd does nothing.
func (t *Tracer) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

// TraceCopyFromStart does nothing.
func (t *Tracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	return ctx
}

// TraceCopyFromEnd records a CopyFrom that copied rows as a write.
func (t *Tracer) TraceCopyFromEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceCopyFromEndData) {
	if data.Err != nil || data.CommandTag.RowsAffected() == 0 {
		return
	}
	t.markWrite(ctx, conn.PgConn().TxStatus())
}

// record records what a statement that ended did, from its command tag.
// txStatus is the connection's after the statement: 'I' outside a
// transaction, 'T' or 'E' in one.
//
// A failed statement records nothing. A failed COMMIT may or may not have
// committed, so the write is kept until the scope's next commit or
// rollback, which reads from the primary more than needed at worst.
func (t *Tracer) record(ctx context.Context, txStatus byte, tag pgconn.CommandTag, err error) {
	if err != nil {
		return
	}
	switch {
	case isWrite(tag):
		if tag.RowsAffected() > 0 {
			t.markWrite(ctx, txStatus)
		}
	case tag.String() == "COMMIT":
		t.recorder.MarkCommitted(ctx)
	// ROLLBACK TO SAVEPOINT has the tag ROLLBACK too, and leaves the
	// transaction open. Only a rollback that ends it drops the writes.
	case tag.String() == "ROLLBACK" && txStatus == 'I':
		t.recorder.MarkRolledBack(ctx)
	}
}

// markWrite records a write, as uncommitted while in a transaction.
func (t *Tracer) markWrite(ctx context.Context, txStatus byte) {
	if txStatus == 'I' {
		t.recorder.MarkWritten(ctx)
	} else {
		t.recorder.MarkUncommitted(ctx)
	}
}

// isWrite reports whether tag is of a statement that writes rows.
// CommandTag has no method for MERGE, so its prefix is checked.
func isWrite(tag pgconn.CommandTag) bool {
	return tag.Insert() || tag.Update() || tag.Delete() || strings.HasPrefix(tag.String(), "MERGE ")
}

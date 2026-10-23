// Package sqlstick records the writes of a database/sql driver for a
// dbstick Tracker. It wraps the primary's driver, so the primary is
// still a plain *sql.DB:
//
//	primary := sql.OpenDB(sqlstick.WrapConnector(connector, tracker))
//
// tracker is a *dbstick.Tracker of github.com/mpyw/dbstick, or any other
// [Recorder].
//
// A write outside a transaction is recorded when it succeeds. A write in
// a transaction makes the scope sticky at once, and is put in the Store
// when the transaction commits. A rollback drops it.
//
// For PostgreSQL through pgx, use github.com/mpyw/dbstick/pgxstick
// instead. It reads what each statement did from the server, where this
// package has to guess for a query.
package sqlstick

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
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

// WrapConnector returns a connector whose connections record their
// writes in recorder. Pass it to sql.OpenDB. It panics if recorder is nil.
//
// A write is an Exec that affected at least one row, or a Query that
// starts with INSERT, UPDATE, DELETE, MERGE or REPLACE, such as INSERT ...
// RETURNING. An Exec whose driver cannot count the rows counts as a
// write. Any other write is not seen, such as a write in a WITH clause or
// in a stored procedure. Call MarkWritten of the Recorder for those.
//
// sql.Conn.Raw passes the wrapping connection, not the driver's.
func WrapConnector(connector driver.Connector, recorder Recorder) driver.Connector {
	if recorder == nil {
		panic("sqlstick: WrapConnector: recorder is nil")
	}
	return &wrappedConnector{inner: connector, recorder: recorder}
}

// WrapDriver is [WrapConnector] for a driver and a data source name, as
// sql.Open takes them. Use it for a driver that has no connector of its
// own. It panics if recorder is nil.
func WrapDriver(d driver.Driver, dsn string, recorder Recorder) (driver.Connector, error) {
	if recorder == nil {
		panic("sqlstick: WrapDriver: recorder is nil")
	}
	if dc, ok := d.(driver.DriverContext); ok {
		connector, err := dc.OpenConnector(dsn)
		if err != nil {
			return nil, err
		}
		return WrapConnector(connector, recorder), nil
	}
	return WrapConnector(wrappedDSNConnector{driver: d, dsn: dsn}, recorder), nil
}

// wrappedDSNConnector is the connector of a driver with no
// driver.DriverContext. It is what database/sql uses for one.
type wrappedDSNConnector struct {
	driver driver.Driver
	dsn    string
}

func (c wrappedDSNConnector) Connect(context.Context) (driver.Conn, error) {
	return c.driver.Open(c.dsn)
}

func (c wrappedDSNConnector) Driver() driver.Driver {
	return c.driver
}

type wrappedConnector struct {
	inner    driver.Connector
	recorder Recorder
}

var _ io.Closer = (*wrappedConnector)(nil)

func (c *wrappedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &wrappedConn{inner: conn, recorder: c.recorder}, nil
}

func (c *wrappedConnector) Driver() driver.Driver {
	return c.inner.Driver()
}

// Close closes the inner connector if it is an io.Closer, as sql.DB.Close
// does for a connector.
func (c *wrappedConnector) Close() error {
	if closer, ok := c.inner.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// wrappedConn records the writes of a connection. database/sql uses a
// connection from one goroutine at a time, so inTx needs no lock.
//
// It implements every optional interface of a connection. Each one does
// what database/sql does when the inner connection lacks it.
type wrappedConn struct {
	inner    driver.Conn
	recorder Recorder
	// inTx is set while a transaction is open on the connection.
	inTx bool
}

var (
	_ driver.ConnBeginTx        = (*wrappedConn)(nil)
	_ driver.ConnPrepareContext = (*wrappedConn)(nil)
	_ driver.ExecerContext      = (*wrappedConn)(nil)
	_ driver.QueryerContext     = (*wrappedConn)(nil)
	_ driver.Pinger             = (*wrappedConn)(nil)
	_ driver.SessionResetter    = (*wrappedConn)(nil)
	_ driver.Validator          = (*wrappedConn)(nil)
	_ driver.NamedValueChecker  = (*wrappedConn)(nil)
)

func (c *wrappedConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *wrappedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var (
		s   driver.Stmt
		err error
	)
	if pc, ok := c.inner.(driver.ConnPrepareContext); ok {
		s, err = pc.PrepareContext(ctx, query)
	} else {
		s, err = c.inner.Prepare(query)
		if err == nil && ctx.Err() != nil {
			_ = s.Close() // ctx's error is the one to return
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return c.wrapStmt(s, query), nil
}

func (c *wrappedConn) Close() error {
	return c.inner.Close()
}

func (c *wrappedConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *wrappedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	var (
		inner driver.Tx
		err   error
	)
	if bc, ok := c.inner.(driver.ConnBeginTx); ok {
		inner, err = bc.BeginTx(ctx, opts)
	} else {
		inner, err = c.beginWithoutContext(ctx, opts)
	}
	if err != nil {
		return nil, err
	}
	c.inTx = true
	return &wrappedTx{inner: inner, conn: c, ctx: ctx}, nil
}

// beginWithoutContext begins a transaction on a connection with no
// driver.ConnBeginTx, as database/sql does.
func (c *wrappedConn) beginWithoutContext(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if opts.Isolation != driver.IsolationLevel(0) {
		return nil, errors.New("sql: driver does not support non-default isolation level")
	}
	if opts.ReadOnly {
		return nil, errors.New("sql: driver does not support read-only transactions")
	}
	inner, err := c.inner.Begin() //nolint:staticcheck // the driver has no BeginTx
	if err == nil && ctx.Err() != nil {
		_ = inner.Rollback() // ctx's error is the one to return
		return nil, ctx.Err()
	}
	return inner, err
}

func (c *wrappedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	var (
		res driver.Result
		err error
	)
	switch inner := c.inner.(type) {
	case driver.ExecerContext:
		res, err = inner.ExecContext(ctx, query, args)
	case driver.Execer: //nolint:staticcheck // the driver has no ExecContext
		res, err = callLegacy(ctx, args, func(values []driver.Value) (driver.Result, error) {
			return inner.Exec(query, values)
		})
	default:
		// database/sql prepares the statement instead.
		return nil, driver.ErrSkip
	}
	if err != nil {
		return nil, err
	}
	c.recordExec(ctx, res)
	return res, nil
}

func (c *wrappedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	var (
		rows driver.Rows
		err  error
	)
	switch inner := c.inner.(type) {
	case driver.QueryerContext:
		rows, err = inner.QueryContext(ctx, query, args)
	case driver.Queryer: //nolint:staticcheck // the driver has no QueryContext
		rows, err = callLegacy(ctx, args, func(values []driver.Value) (driver.Rows, error) {
			return inner.Query(query, values)
		})
	default:
		// database/sql prepares the statement instead.
		return nil, driver.ErrSkip
	}
	if err != nil {
		return nil, err
	}
	c.recordQuery(ctx, query)
	return rows, nil
}

func (c *wrappedConn) Ping(ctx context.Context) error {
	if p, ok := c.inner.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *wrappedConn) ResetSession(ctx context.Context) error {
	if r, ok := c.inner.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *wrappedConn) IsValid() bool {
	if v, ok := c.inner.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// CheckNamedValue passes nv to the inner connection's checker. Without
// one, driver.ErrSkip makes database/sql convert nv as it would.
func (c *wrappedConn) CheckNamedValue(nv *driver.NamedValue) error {
	if checker, ok := c.inner.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

// recordExec records res as a write if it affected rows, or if the driver
// cannot tell.
func (c *wrappedConn) recordExec(ctx context.Context, res driver.Result) {
	if n, err := res.RowsAffected(); err != nil || n > 0 {
		c.markWrite(ctx)
	}
}

// recordQuery records query as a write if it looks like one.
func (c *wrappedConn) recordQuery(ctx context.Context, query string) {
	if startsWithWriteKeyword(query) {
		c.markWrite(ctx)
	}
}

// markWrite records a write, as uncommitted while in a transaction.
func (c *wrappedConn) markWrite(ctx context.Context) {
	if c.inTx {
		c.recorder.MarkUncommitted(ctx)
	} else {
		c.recorder.MarkWritten(ctx)
	}
}

// wrapStmt wraps s. A driver.ColumnConverter changes how database/sql
// converts arguments, so the wrapper has one only if s has.
func (c *wrappedConn) wrapStmt(s driver.Stmt, query string) driver.Stmt {
	ws := &wrappedStmt{inner: s, conn: c, query: query}
	if cc, ok := s.(driver.ColumnConverter); ok { //nolint:staticcheck // the driver may still have it
		return &wrappedConverterStmt{wrappedStmt: ws, converter: cc}
	}
	return ws
}

// wrappedStmt records the writes of a prepared statement.
type wrappedStmt struct {
	inner driver.Stmt
	conn  *wrappedConn
	query string
}

var (
	_ driver.StmtExecContext   = (*wrappedStmt)(nil)
	_ driver.StmtQueryContext  = (*wrappedStmt)(nil)
	_ driver.NamedValueChecker = (*wrappedStmt)(nil)
)

func (s *wrappedStmt) Close() error {
	return s.inner.Close()
}

func (s *wrappedStmt) NumInput() int {
	return s.inner.NumInput()
}

func (s *wrappedStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), s.named(args))
}

func (s *wrappedStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), s.named(args))
}

// named turns the arguments of Exec or Query into those of their context
// forms.
func (s *wrappedStmt) named(args []driver.Value) []driver.NamedValue {
	named := make([]driver.NamedValue, len(args))
	for i, v := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return named
}

func (s *wrappedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	var (
		res driver.Result
		err error
	)
	if ec, ok := s.inner.(driver.StmtExecContext); ok {
		res, err = ec.ExecContext(ctx, args)
	} else {
		res, err = callLegacy(ctx, args, s.inner.Exec) //nolint:staticcheck // the driver has no ExecContext
	}
	if err != nil {
		return nil, err
	}
	s.conn.recordExec(ctx, res)
	return res, nil
}

func (s *wrappedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	var (
		rows driver.Rows
		err  error
	)
	if qc, ok := s.inner.(driver.StmtQueryContext); ok {
		rows, err = qc.QueryContext(ctx, args)
	} else {
		rows, err = callLegacy(ctx, args, s.inner.Query) //nolint:staticcheck // the driver has no QueryContext
	}
	if err != nil {
		return nil, err
	}
	s.conn.recordQuery(ctx, s.query)
	return rows, nil
}

// CheckNamedValue passes nv to the checker that database/sql would use:
// the inner statement's, or else the inner connection's. database/sql asks
// the connection only when the statement has no checker, and this
// statement always has one.
func (s *wrappedStmt) CheckNamedValue(nv *driver.NamedValue) error {
	if checker, ok := s.inner.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return s.conn.CheckNamedValue(nv)
}

// wrappedConverterStmt is a wrappedStmt whose inner statement has a
// driver.ColumnConverter.
type wrappedConverterStmt struct {
	*wrappedStmt
	converter driver.ColumnConverter //nolint:staticcheck // the driver may still have it
}

func (s *wrappedConverterStmt) ColumnConverter(idx int) driver.ValueConverter {
	return s.converter.ColumnConverter(idx)
}

// wrappedTx records the end of a transaction. ctx is the one BeginTx got,
// since Commit and Rollback get none.
type wrappedTx struct {
	inner driver.Tx
	conn  *wrappedConn
	ctx   context.Context
}

// Commit puts the transaction's writes in the Store if it committed. A
// failed commit may or may not have committed, so its writes are kept
// until the scope's next commit or rollback.
func (t *wrappedTx) Commit() error {
	t.conn.inTx = false
	if err := t.inner.Commit(); err != nil {
		return err
	}
	t.conn.recorder.MarkCommitted(t.ctx)
	return nil
}

func (t *wrappedTx) Rollback() error {
	t.conn.inTx = false
	t.conn.recorder.MarkRolledBack(t.ctx)
	return t.inner.Rollback()
}

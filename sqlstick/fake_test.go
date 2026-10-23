package sqlstick

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"slices"
	"testing"
)

//declscope:shared // wrap_test.go drives the wrapper with it
var errFake = errors.New("fake failure")

// fakeState is what a fake connection returns, and what it saw.
//
//declscope:shared // wrap_test.go drives the wrapper with it
type fakeState struct {
	// err fails every call that can fail.
	err error
	// rowsAffected and rowsAffectedErr are the result of each Exec.
	rowsAffected    int64
	rowsAffectedErr error
	// commitErr fails Commit.
	commitErr error
	// ctxStmt makes Prepare return a statement with context methods.
	ctxStmt bool
	// cancelOnPrepare and cancelOnBegin cancel this context during the
	// call, as a context can be canceled at any time.
	cancel          context.CancelFunc
	cancelOnPrepare bool
	cancelOnBegin   bool
	// closed and rolledBack count the calls.
	closed, rolledBack int
}

// fakeConn has only the methods that every driver.Conn has.
//
//declscope:shared // wrap_test.go drives the wrapper with it
type fakeConn struct{ st *fakeState }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	if c.st.err != nil {
		return nil, c.st.err
	}
	if c.st.cancelOnPrepare {
		c.st.cancel()
	}
	s := &fakeStmt{st: c.st}
	if c.st.ctxStmt {
		return &fakeCtxStmt{s}, nil
	}
	return s, nil
}

func (c *fakeConn) Close() error {
	c.st.closed++
	return nil
}

func (c *fakeConn) Begin() (driver.Tx, error) {
	if c.st.err != nil {
		return nil, c.st.err
	}
	if c.st.cancelOnBegin {
		c.st.cancel()
	}
	return &fakeTx{st: c.st}, nil
}

func (c *fakeConn) result() (driver.Result, error) {
	if c.st.err != nil {
		return nil, c.st.err
	}
	return fakeResult{c.st}, nil
}

func (c *fakeConn) rows() (driver.Rows, error) {
	if c.st.err != nil {
		return nil, c.st.err
	}
	return &fakeRows{}, nil
}

// fakeLegacyConn has the methods that drivers had before contexts.
//
//declscope:shared // wrap_test.go drives the wrapper with it
type fakeLegacyConn struct{ *fakeConn }

func (c *fakeLegacyConn) Exec(string, []driver.Value) (driver.Result, error) {
	return c.result()
}

func (c *fakeLegacyConn) Query(string, []driver.Value) (driver.Rows, error) {
	return c.rows()
}

// fakeCtxConn has every optional method of a connection.
//
//declscope:shared // wrap_test.go drives the wrapper with it
type fakeCtxConn struct{ *fakeConn }

func (c *fakeCtxConn) PrepareContext(_ context.Context, query string) (driver.Stmt, error) {
	return c.Prepare(query)
}

func (c *fakeCtxConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}

func (c *fakeCtxConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return c.result()
}

func (c *fakeCtxConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return c.rows()
}

func (c *fakeCtxConn) Ping(context.Context) error               { return c.st.err }
func (c *fakeCtxConn) ResetSession(context.Context) error       { return c.st.err }
func (c *fakeCtxConn) IsValid() bool                            { return c.st.err == nil }
func (c *fakeCtxConn) CheckNamedValue(*driver.NamedValue) error { return errFake }

type fakeStmt struct{ st *fakeState }

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }

func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return (&fakeConn{s.st}).result()
}

func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	return (&fakeConn{s.st}).rows()
}

// fakeCtxStmt has every optional method of a statement.
type fakeCtxStmt struct{ *fakeStmt }

func (s *fakeCtxStmt) ExecContext(context.Context, []driver.NamedValue) (driver.Result, error) {
	return s.Exec(nil)
}

func (s *fakeCtxStmt) QueryContext(context.Context, []driver.NamedValue) (driver.Rows, error) {
	return s.Query(nil)
}

func (s *fakeCtxStmt) CheckNamedValue(*driver.NamedValue) error { return errFake }

func (s *fakeCtxStmt) ColumnConverter(int) driver.ValueConverter { //nolint:staticcheck // to test it
	return driver.Bool
}

type fakeTx struct{ st *fakeState }

func (t *fakeTx) Commit() error { return t.st.commitErr }

func (t *fakeTx) Rollback() error {
	t.st.rolledBack++
	return nil
}

type fakeResult struct{ st *fakeState }

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }

func (r fakeResult) RowsAffected() (int64, error) {
	return r.st.rowsAffected, r.st.rowsAffectedErr
}

type fakeRows struct{}

func (r *fakeRows) Columns() []string         { return nil }
func (r *fakeRows) Close() error              { return nil }
func (r *fakeRows) Next([]driver.Value) error { return io.EOF }

// fakeDriver opens conn, or fails with err.
//
//declscope:shared // wrap_test.go drives the wrapper with it
type fakeDriver struct {
	conn driver.Conn
	err  error
}

func (d fakeDriver) Open(string) (driver.Conn, error) {
	return d.conn, d.err
}

// fakeContextDriver has a connector of its own.
//
//declscope:shared // wrap_test.go drives the wrapper with it
type fakeContextDriver struct {
	fakeDriver
	connectorErr error
}

func (d fakeContextDriver) OpenConnector(string) (driver.Connector, error) {
	if d.connectorErr != nil {
		return nil, d.connectorErr
	}
	return fakeConnector{driver: d}, nil
}

// fakeConnector opens connections of driver.
type fakeConnector struct {
	driver driver.Driver
}

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) {
	return c.driver.Open("")
}

func (c fakeConnector) Driver() driver.Driver {
	return c.driver
}

// fakeClosingConnector is a connector that is an io.Closer.
//
//declscope:shared // wrap_test.go drives the wrapper with it
type fakeClosingConnector struct {
	fakeConnector
	closed bool
}

func (c *fakeClosingConnector) Close() error {
	c.closed = true
	return nil
}

// fakeRequestKey marks the context of the request under test.
type fakeRequestKey struct{}

// fakeRecorder keeps the names of the calls made with the request's
// context. Calls with another context show as missing.
//
//declscope:shared // wrap_test.go drives the wrapper with it
type fakeRecorder struct {
	// calls are the names of the calls, in order.
	//
	//declscope:private // only its methods read it
	calls []string
}

func (r *fakeRecorder) add(ctx context.Context, name string) {
	if ctx.Value(fakeRequestKey{}) != nil {
		r.calls = append(r.calls, name)
	}
}

func (r *fakeRecorder) MarkWritten(ctx context.Context)     { r.add(ctx, "MarkWritten") }
func (r *fakeRecorder) MarkUncommitted(ctx context.Context) { r.add(ctx, "MarkUncommitted") }
func (r *fakeRecorder) MarkCommitted(ctx context.Context)   { r.add(ctx, "MarkCommitted") }
func (r *fakeRecorder) MarkRolledBack(ctx context.Context)  { r.add(ctx, "MarkRolledBack") }

//declscope:shared // wrap_test.go drives the wrapper with it
func (r *fakeRecorder) wantCalls(t *testing.T, want ...string) {
	t.Helper()
	if !slices.Equal(r.calls, want) {
		t.Errorf("calls = %v, want %v", r.calls, want)
	}
}

// newFake returns a recorder, and a context of the request that the
// fake's state can cancel.
//
//declscope:shared // wrap_test.go drives the wrapper with it
func newFake(t *testing.T, st *fakeState) (*fakeRecorder, context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), fakeRequestKey{}, true))
	t.Cleanup(cancel)
	st.cancel = cancel
	return &fakeRecorder{}, ctx
}

// openFake opens conn through database/sql.
//
//declscope:shared // wrap_test.go drives the wrapper with it
func openFake(t *testing.T, rec Recorder, conn driver.Conn) *sql.DB {
	t.Helper()
	connector, err := WrapDriver(fakeDriver{conn: conn}, "", rec)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() }) // the test is over
	return db
}

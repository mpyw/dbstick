package sqlstick

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
)

func TestWrapPanicsOnNil(t *testing.T) {
	for name, f := range map[string]func(){
		"WrapConnector": func() { WrapConnector(wrappedDSNConnector{}, nil) },
		"WrapDriver":    func() { _, _ = WrapDriver(fakeDriver{}, "", nil) }, // it panics first
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("did not panic")
				}
			}()
			f()
		})
	}
}

func TestWrapDriver(t *testing.T) {
	rec := &fakeRecorder{}
	conn := &fakeConn{&fakeState{}}
	connector, err := WrapDriver(fakeContextDriver{fakeDriver: fakeDriver{conn: conn}}, "dsn", rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := connector.Driver().(fakeContextDriver); !ok {
		t.Errorf("Driver() = %T, want the driver", connector.Driver())
	}
	got, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if wc, ok := got.(*wrappedConn); !ok || wc.inner != conn {
		t.Errorf("Connect() = %#v, want the connection wrapped", got)
	}

	_, err = WrapDriver(fakeContextDriver{connectorErr: errFake}, "dsn", rec)
	if !errors.Is(err, errFake) {
		t.Errorf("WrapDriver with a failing OpenConnector = %v, want %v", err, errFake)
	}
	connector, err = WrapDriver(fakeDriver{err: errFake}, "dsn", rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := connector.Driver().(fakeDriver); !ok {
		t.Errorf("Driver() = %T, want the driver", connector.Driver())
	}
	if _, err := connector.Connect(context.Background()); !errors.Is(err, errFake) {
		t.Errorf("Connect with a failing Open = %v, want %v", err, errFake)
	}
}

func TestWrapClose(t *testing.T) {
	closing := &fakeClosingConnector{}
	for _, inner := range []driver.Connector{closing, wrappedDSNConnector{}} {
		closer, ok := WrapConnector(inner, &fakeRecorder{}).(io.Closer)
		if !ok {
			t.Fatal("the connector is no io.Closer")
		}
		if err := closer.Close(); err != nil {
			t.Errorf("Close of %T = %v", inner, err)
		}
	}
	if !closing.closed {
		t.Error("the inner io.Closer was not closed")
	}
}

func TestWrapExec(t *testing.T) {
	for _, tc := range []struct {
		name string
		conn func(*fakeState) driver.Conn
	}{
		{"ExecerContext", func(st *fakeState) driver.Conn { return &fakeCtxConn{&fakeConn{st}} }},
		{"Execer", func(st *fakeState) driver.Conn { return &fakeLegacyConn{&fakeConn{st}} }},
		{"prepared by database/sql", func(st *fakeState) driver.Conn { return &fakeConn{st} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, rc := range []struct {
				name string
				n    int64
				err  error
				want []string
			}{
				{"rows", 1, nil, []string{"MarkWritten"}},
				{"no rows", 0, nil, nil},
				{"rows unknown", 0, errFake, []string{"MarkWritten"}},
			} {
				t.Run(rc.name, func(t *testing.T) {
					st := &fakeState{rowsAffected: rc.n, rowsAffectedErr: rc.err}
					rec, ctx := newFake(t, st)
					db := openFake(t, rec, tc.conn(st))
					if _, err := db.ExecContext(ctx, "UPDATE t SET a = 1"); err != nil {
						t.Fatal(err)
					}
					rec.wantCalls(t, rc.want...)
				})
			}
		})
	}
}

func TestWrapQuery(t *testing.T) {
	for _, tc := range []struct {
		name string
		conn func(*fakeState) driver.Conn
	}{
		{"QueryerContext", func(st *fakeState) driver.Conn { return &fakeCtxConn{&fakeConn{st}} }},
		{"Queryer", func(st *fakeState) driver.Conn { return &fakeLegacyConn{&fakeConn{st}} }},
		{"prepared by database/sql", func(st *fakeState) driver.Conn { return &fakeConn{st} }},
		{"prepared with contexts", func(st *fakeState) driver.Conn {
			st.ctxStmt = true
			return &fakeConn{st}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for query, want := range map[string][]string{
				"INSERT INTO t VALUES (1) RETURNING id": {"MarkWritten"},
				"SELECT 1":                              nil,
			} {
				t.Run(query, func(t *testing.T) {
					st := &fakeState{}
					rec, ctx := newFake(t, st)
					db := openFake(t, rec, tc.conn(st))
					rows, err := db.QueryContext(ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					_ = rows.Close() // nothing to read
					rec.wantCalls(t, want...)
				})
			}
		})
	}
}

func TestWrapErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		conn func(*fakeState) driver.Conn
	}{
		{"with contexts", func(st *fakeState) driver.Conn {
			st.ctxStmt = true
			return &fakeCtxConn{&fakeConn{st}}
		}},
		{"without contexts", func(st *fakeState) driver.Conn { return &fakeLegacyConn{&fakeConn{st}} }},
		{"prepared", func(st *fakeState) driver.Conn { return &fakeConn{st} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeState{rowsAffected: 1}
			rec, ctx := newFake(t, st)
			c := &wrappedConn{inner: tc.conn(st), recorder: rec}
			stmt, err := c.PrepareContext(ctx, "INSERT INTO t VALUES (1) RETURNING id")
			if err != nil {
				t.Fatal(err)
			}
			st.err = errFake
			if _, err := c.PrepareContext(ctx, "INSERT"); !errors.Is(err, errFake) {
				t.Errorf("PrepareContext = %v, want %v", err, errFake)
			}
			if _, err := c.BeginTx(ctx, driver.TxOptions{}); !errors.Is(err, errFake) {
				t.Errorf("BeginTx = %v, want %v", err, errFake)
			}
			// Without an Execer, the error is driver.ErrSkip.
			if _, err := c.ExecContext(ctx, "INSERT", nil); err == nil {
				t.Error("ExecContext: no error")
			}
			if _, err := c.QueryContext(ctx, "INSERT", nil); err == nil {
				t.Error("QueryContext: no error")
			}
			ws, ok := stmt.(interface {
				driver.StmtExecContext
				driver.StmtQueryContext
			})
			if !ok {
				t.Fatalf("PrepareContext = %T, want context methods", stmt)
			}
			if _, err := ws.ExecContext(ctx, nil); !errors.Is(err, errFake) {
				t.Errorf("Stmt.ExecContext = %v, want %v", err, errFake)
			}
			if _, err := ws.QueryContext(ctx, nil); !errors.Is(err, errFake) {
				t.Errorf("Stmt.QueryContext = %v, want %v", err, errFake)
			}
			rec.wantCalls(t)
		})
	}
}

func TestWrapSkipsWithoutExecer(t *testing.T) {
	c := &wrappedConn{inner: &fakeConn{&fakeState{}}}
	if _, err := c.ExecContext(context.Background(), "", nil); !errors.Is(err, driver.ErrSkip) {
		t.Errorf("ExecContext = %v, want %v", err, driver.ErrSkip)
	}
	if _, err := c.QueryContext(context.Background(), "", nil); !errors.Is(err, driver.ErrSkip) {
		t.Errorf("QueryContext = %v, want %v", err, driver.ErrSkip)
	}
}

func TestWrapLegacyMethods(t *testing.T) {
	st := &fakeState{rowsAffected: 1}
	rec, _ := newFake(t, st)
	c := &wrappedConn{inner: &fakeConn{st}, recorder: rec}
	stmt, err := c.Prepare("INSERT INTO t VALUES (?)")
	if err != nil {
		t.Fatal(err)
	}
	if n := stmt.NumInput(); n != -1 {
		t.Errorf("NumInput = %d, want the inner -1", n)
	}
	if _, err := stmt.Exec([]driver.Value{1}); err != nil { //nolint:staticcheck // to test it
		t.Error(err)
	}
	if _, err := stmt.Query([]driver.Value{1}); err != nil { //nolint:staticcheck // to test it
		t.Error(err)
	}
	if err := stmt.Close(); err != nil {
		t.Error(err)
	}
	tx, err := c.Begin() //nolint:staticcheck // to test it
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Error(err)
	}
	if err := c.Close(); err != nil || st.closed != 1 {
		t.Errorf("Close = %v, closed %d times, want once", err, st.closed)
	}
}

func TestWrapNamedArguments(t *testing.T) {
	st := &fakeState{}
	c := &wrappedConn{inner: &fakeLegacyConn{&fakeConn{st}}}
	named := []driver.NamedValue{{Name: "a", Ordinal: 1, Value: 1}}
	if _, err := c.ExecContext(context.Background(), "", named); err == nil {
		t.Error("ExecContext with a name: no error")
	}
	if _, err := c.QueryContext(context.Background(), "", named); err == nil {
		t.Error("QueryContext with a name: no error")
	}
}

func TestWrapCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st := &fakeState{}
	c := &wrappedConn{inner: &fakeLegacyConn{&fakeConn{st}}}
	if _, err := c.ExecContext(ctx, "", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("ExecContext = %v, want %v", err, context.Canceled)
	}
	if _, err := c.QueryContext(ctx, "", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("QueryContext = %v, want %v", err, context.Canceled)
	}

	// A context canceled during the call undoes it.
	st.cancelOnPrepare, st.cancelOnBegin = true, true
	for _, call := range []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := c.PrepareContext(ctx, "")
			return err
		},
		func(ctx context.Context) error {
			_, err := c.BeginTx(ctx, driver.TxOptions{})
			return err
		},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		st.cancel = cancel
		if err := call(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("call = %v, want %v", err, context.Canceled)
		}
	}
	if st.rolledBack != 1 {
		t.Errorf("rolled back %d times, want once", st.rolledBack)
	}
}

func TestWrapBeginOptions(t *testing.T) {
	c := &wrappedConn{inner: &fakeConn{&fakeState{}}}
	for _, opts := range []driver.TxOptions{
		{Isolation: driver.IsolationLevel(sql.LevelSerializable)},
		{ReadOnly: true},
	} {
		if _, err := c.BeginTx(context.Background(), opts); err == nil {
			t.Errorf("BeginTx(%+v) without ConnBeginTx: no error", opts)
		}
	}
	c = &wrappedConn{inner: &fakeCtxConn{&fakeConn{&fakeState{}}}}
	if _, err := c.BeginTx(context.Background(), driver.TxOptions{ReadOnly: true}); err != nil {
		t.Errorf("BeginTx with ConnBeginTx = %v", err)
	}
}

func TestWrapTransaction(t *testing.T) {
	st := &fakeState{rowsAffected: 1}
	rec, ctx := newFake(t, st)
	db := openFake(t, rec, &fakeCtxConn{&fakeConn{st}})

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE t SET a = 1"); err != nil {
		t.Fatal(err)
	}
	st.commitErr = errFake
	if err := tx.Commit(); !errors.Is(err, errFake) {
		t.Errorf("Commit = %v, want %v", err, errFake)
	}
	// A failed commit may or may not have committed, so it records
	// nothing. The Tracker keeps the write for the next commit.
	rec.wantCalls(t, "MarkUncommitted")
	st.commitErr = nil
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rec.wantCalls(t, "MarkUncommitted", "MarkCommitted")
}

func TestWrapOptionalMethods(t *testing.T) {
	plain := &wrappedConn{inner: &fakeConn{&fakeState{}}}
	if err := plain.Ping(context.Background()); err != nil {
		t.Errorf("Ping = %v, want nil", err)
	}
	if err := plain.ResetSession(context.Background()); err != nil {
		t.Errorf("ResetSession = %v, want nil", err)
	}
	if !plain.IsValid() {
		t.Error("IsValid = false, want true")
	}
	if err := plain.CheckNamedValue(&driver.NamedValue{}); !errors.Is(err, driver.ErrSkip) {
		t.Errorf("CheckNamedValue = %v, want %v", err, driver.ErrSkip)
	}

	st := &fakeState{err: errFake}
	full := &wrappedConn{inner: &fakeCtxConn{&fakeConn{st}}}
	if err := full.Ping(context.Background()); !errors.Is(err, errFake) {
		t.Errorf("Ping = %v, want %v", err, errFake)
	}
	if err := full.ResetSession(context.Background()); !errors.Is(err, errFake) {
		t.Errorf("ResetSession = %v, want %v", err, errFake)
	}
	if full.IsValid() {
		t.Error("IsValid = true, want the inner false")
	}
	if err := full.CheckNamedValue(&driver.NamedValue{}); !errors.Is(err, errFake) {
		t.Errorf("CheckNamedValue = %v, want %v", err, errFake)
	}
}

func TestWrapStmtCheckers(t *testing.T) {
	st := &fakeState{ctxStmt: true}
	c := &wrappedConn{inner: &fakeConn{st}}
	stmt, err := c.Prepare("")
	if err != nil {
		t.Fatal(err)
	}
	cs, ok := stmt.(*wrappedConverterStmt)
	if !ok {
		t.Fatalf("Prepare = %T, want a *wrappedConverterStmt", stmt)
	}
	if got := cs.ColumnConverter(0); got != driver.Bool {
		t.Errorf("ColumnConverter = %v, want the inner one", got)
	}
	if err := cs.CheckNamedValue(&driver.NamedValue{}); !errors.Is(err, errFake) {
		t.Errorf("CheckNamedValue = %v, want the statement's %v", err, errFake)
	}

	// A statement with no checker asks the connection's.
	st.ctxStmt = false
	stmt, err = (&wrappedConn{inner: &fakeCtxConn{&fakeConn{st}}}).Prepare("")
	if err != nil {
		t.Fatal(err)
	}
	checker, ok := stmt.(driver.NamedValueChecker)
	if !ok {
		t.Fatalf("Prepare = %T, want a driver.NamedValueChecker", stmt)
	}
	if err := checker.CheckNamedValue(&driver.NamedValue{}); !errors.Is(err, errFake) {
		t.Errorf("CheckNamedValue = %v, want the connection's %v", err, errFake)
	}
}

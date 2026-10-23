[← dbstick](../README.md)

# sqlstick

A [dbstick](../README.md) hook for any `database/sql` driver. It records each write on the primary in a Tracker.

It wraps the primary's driver. The primary is still a plain `*sql.DB`.

## Install

```sh
go get github.com/mpyw/dbstick/sqlstick
```

## Usage

Step 1 writes `openTrackedDB`, which opens the primary with the hook. Pick the one for your driver. Step 2 uses it.

### 1: MySQL

Wrap the driver's connector with `WrapConnector`:

```go
import (
	"database/sql"

	"github.com/go-sql-driver/mysql"
	"github.com/mpyw/dbstick"
	"github.com/mpyw/dbstick/sqlstick"
)

// openTrackedDB opens the primary with the hook.
func openTrackedDB(
	dsn string,
	tracker *dbstick.Tracker,
) *sql.DB {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		panic(err)
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		panic(err)
	}
	wrapped := sqlstick.WrapConnector(connector, tracker)
	return sql.OpenDB(wrapped)
}
```

### 1: SQLite

The driver has no connector of its own, so pass the driver and the DSN to `WrapDriver`:

```go
import (
	"database/sql"

	"github.com/mpyw/dbstick"
	"github.com/mpyw/dbstick/sqlstick"
	"modernc.org/sqlite"
)

// openTrackedDB opens the primary with the hook.
func openTrackedDB(
	dsn string,
	tracker *dbstick.Tracker,
) *sql.DB {
	connector, err := sqlstick.WrapDriver(
		&sqlite.Driver{},
		dsn,
		tracker,
	)
	if err != nil {
		panic(err)
	}
	return sql.OpenDB(connector)
}
```

### 2: Make the Router

The replica gets no hook. Open it with the same driver:

```go
primary := openTrackedDB(primaryDSN, tracker)
replica, err := sql.Open("mysql", replicaDSN) // or "sqlite"
if err != nil {
	panic(err)
}

db := dbstick.NewRouter(tracker, primary, replica)
```

## How it works

### Writes

| Call | Recorded when |
| --- | --- |
| Exec | It changed at least one row, or the driver cannot count the rows |
| Query | Its SQL starts with INSERT, UPDATE, DELETE, MERGE or REPLACE |
| A failed call | Never |

A Query has no row count, so sqlstick reads the first word of its SQL. Spaces, comments and opening parentheses before it are skipped. This finds `INSERT ... RETURNING`. It does not find a write in a `WITH` clause, or in a stored procedure.

For a write it does not see, call `tracker.MarkWritten(ctx)` yourself.

### Transactions

| Event | What happens |
| --- | --- |
| A write in a transaction | The current request is sticky at once |
| Commit | The next requests are sticky. The TTL starts here |
| Rollback | The write is dropped |
| A failed commit | The write is kept, since it may have committed |

`database/sql` commits with no context. sqlstick uses the context that `BeginTx` got.

## Notes

- For PostgreSQL through pgx, use [pgxstick](../pgxstick/README.md) instead. It reads what each statement did from the server, so it does not guess.
- `sql.Conn.Raw` passes the wrapping connection, not the driver's.
- The wrapper does what `database/sql` does for a driver that lacks an optional method. For example, an Exec on a driver with no `ExecContext` still works.

## Interface

`WrapConnector` and `WrapDriver` take a `Recorder`. `*dbstick.Tracker` implements it. sqlstick does not import dbstick, so its versions do not depend on dbstick's.

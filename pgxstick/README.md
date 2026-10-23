[← dbstick](../README.md)

# pgxstick

A [dbstick](../README.md) hook for [pgx](https://github.com/jackc/pgx). It records each write on the primary in a Tracker.

It is a pgx tracer. It also works for `database/sql` with pgx's `stdlib` driver.

## Install

```sh
go get github.com/mpyw/dbstick/pgxstick
```

## Usage

Set the tracer on the primary's config only.

```go
import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mpyw/dbstick"
	"github.com/mpyw/dbstick/pgxstick"
)

// openTrackedPool opens the primary with the hook.
func openTrackedPool(
	ctx context.Context,
	dsn string,
	tracker *dbstick.Tracker,
) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		panic(err)
	}
	cfg.ConnConfig.Tracer = pgxstick.NewTracer(tracker)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		panic(err)
	}
	return pool
}
```

The replica gets no hook:

```go
primary := openTrackedPool(ctx, primaryDSN, tracker)
replica, err := pgxpool.New(ctx, replicaDSN)
if err != nil {
	panic(err)
}

db := dbstick.NewRouter(tracker, primary, replica)
```

For `database/sql`, wrap the pool with pgx's [stdlib](https://pkg.go.dev/github.com/jackc/pgx/v5/stdlib):

```go
primarySQL := stdlib.OpenDBFromPool(primary)
```

A pgx config has one tracer. To use another one too, such as for OpenTelemetry, combine them with [multitracer](https://pkg.go.dev/github.com/jackc/pgx/v5/multitracer) in `openTrackedPool`:

```go
cfg.ConnConfig.Tracer = multitracer.New(
	pgxstick.NewTracer(tracker),
	otelTracer,
)
```

## How it works

### Writes

pgxstick reads the command tag that PostgreSQL returns for each statement.

| Statement | Recorded |
| --- | --- |
| INSERT, UPDATE, DELETE or MERGE that changed at least one row | Yes |
| The same, with no rows changed | No |
| `CopyFrom` of at least one row | Yes |
| A statement in a batch | Like any other statement |
| A failed statement | No |
| SELECT, including one that calls a function that writes | No |
| A write in a `WITH` clause of a SELECT | No |

For a write it does not see, call `tracker.MarkWritten(ctx)` yourself.

### Transactions

| Event | What happens |
| --- | --- |
| A write in a transaction | The current request is sticky at once |
| COMMIT | The next requests are sticky. The TTL starts here |
| ROLLBACK | The write is dropped |
| ROLLBACK TO SAVEPOINT | Nothing. The transaction is still open |

## Interface

`NewTracer` takes a `Recorder`. `*dbstick.Tracker` implements it. pgxstick does not import dbstick, so its versions do not depend on dbstick's.

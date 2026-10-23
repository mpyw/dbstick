[← dbstick](../README.md)

# gormstick

A [dbstick](../README.md) plugin for [GORM](https://gorm.io). It reads from the primary while the request is sticky.

It works with [dbresolver](https://github.com/go-gorm/dbresolver), which picks the primary or a replica for each query.

## Install

```sh
go get github.com/mpyw/dbstick/gormstick
```

## Usage

gormstick does not record writes. Open the primary with a hook, and use it as dbresolver's source. Then register gormstick after dbresolver.

Step 1 writes `openTrackedDialector`, which opens the primary with the hook. Pick the one for your driver. Step 2 uses it.

### 1: MySQL

```go
import (
	"database/sql"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/mpyw/dbstick"
	"github.com/mpyw/dbstick/sqlstick"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// openTrackedDialector opens the primary with the hook.
func openTrackedDialector(
	dsn string,
	tracker *dbstick.Tracker,
) gorm.Dialector {
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		panic(err)
	}
	connector, err := gomysql.NewConnector(cfg)
	if err != nil {
		panic(err)
	}
	wrapped := sqlstick.WrapConnector(connector, tracker)
	conn := sql.OpenDB(wrapped)
	return mysql.New(mysql.Config{Conn: conn})
}
```

### 1: PostgreSQL

GORM's driver uses pgx, so use [pgxstick](../pgxstick/README.md):

```go
import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/mpyw/dbstick"
	"github.com/mpyw/dbstick/pgxstick"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// openTrackedDialector opens the primary with the hook.
func openTrackedDialector(
	dsn string,
	tracker *dbstick.Tracker,
) gorm.Dialector {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		panic(err)
	}
	cfg.ConnConfig.Tracer = pgxstick.NewTracer(tracker)
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		panic(err)
	}
	conn := stdlib.OpenDBFromPool(pool)
	return postgres.New(postgres.Config{Conn: conn})
}
```

### 1: SQLite

The driver has no connector of its own, so pass the driver and the DSN to `WrapDriver`:

```go
import (
	"database/sql"

	"github.com/mattn/go-sqlite3"
	"github.com/mpyw/dbstick"
	"github.com/mpyw/dbstick/sqlstick"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// openTrackedDialector opens the primary with the hook.
func openTrackedDialector(
	dsn string,
	tracker *dbstick.Tracker,
) gorm.Dialector {
	connector, err := sqlstick.WrapDriver(
		&sqlite3.SQLiteDriver{},
		dsn,
		tracker,
	)
	if err != nil {
		panic(err)
	}
	conn := sql.OpenDB(connector)
	return sqlite.New(sqlite.Config{Conn: conn})
}
```

### 2: Register the plugin

Use the primary as the only source. The replica gets no hook. Open it with the same driver:

```go
import (
	"github.com/mpyw/dbstick"
	"github.com/mpyw/dbstick/gormstick"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

// openDB opens GORM with the primary as its source.
func openDB(
	primaryDSN, replicaDSN string,
	tracker *dbstick.Tracker,
) *gorm.DB {
	primary := openTrackedDialector(primaryDSN, tracker)

	db, err := gorm.Open(primary)
	if err != nil {
		panic(err)
	}
	err = db.Use(dbresolver.Register(dbresolver.Config{
		Sources: []gorm.Dialector{primary},
		Replicas: []gorm.Dialector{
			mysql.Open(replicaDSN), // or postgres.Open
		},
	}))
	if err != nil {
		panic(err)
	}
	if err := db.Use(gormstick.New(tracker)); err != nil {
		panic(err)
	}
	return db
}
```

Pass the request's context to each query:

```go
db.WithContext(ctx).First(&user, id)
```

Every source needs the hook. A source without one records nothing, and no error tells you.

## How it works

Before each query, gormstick asks the Tracker whether the request is sticky. If it is, gormstick does what `db.Clauses(dbresolver.Write)` does. A transaction keeps its own connection, sticky or not.

## Notes

- `Use` fails if dbresolver is not registered yet.

## Interface

`New` takes a `Checker`. `*dbstick.Tracker` implements it. gormstick does not import dbstick, so its versions do not depend on dbstick's.

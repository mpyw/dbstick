module github.com/mpyw/dbstick/e2e

go 1.26.0

require (
	github.com/glebarez/sqlite v1.11.0
	github.com/jackc/pgx/v5 v5.11.0
	github.com/mpyw/dbstick v0.0.0
	github.com/mpyw/dbstick/gormstick v0.0.0
	github.com/mpyw/dbstick/pgxstick v0.0.0
	github.com/mpyw/dbstick/redisstore v0.0.0
	github.com/mpyw/dbstick/sqlstick v0.0.0
	gorm.io/gorm v1.31.2
	gorm.io/plugin/dbresolver v1.6.2
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/glebarez/go-sqlite v1.21.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/redis/go-redis/v9 v9.23.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	go.uber.org/atomic v1.12.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.61.0 // indirect
)

// This module is never published, so it builds with the modules in this
// repository.
replace (
	github.com/mpyw/dbstick => ../
	github.com/mpyw/dbstick/gormstick => ../gormstick
	github.com/mpyw/dbstick/pgxstick => ../pgxstick
	github.com/mpyw/dbstick/redisstore => ../redisstore
	github.com/mpyw/dbstick/sqlstick => ../sqlstick
)

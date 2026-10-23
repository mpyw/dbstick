// Package dbstick sends reads to the primary database for a while after a
// write, so that a user reads what they just wrote, even on their next
// request. It is a Go port of mpyw/laravel-cached-database-stickiness.
//
// A [Tracker] remembers writes, per key such as a user ID, in a [Store]
// that every app server shares. A [Router] picks the primary or the
// replica from what the Tracker remembers. It does not wrap the database,
// so it works with *sql.DB, *pgxpool.Pool, or any other handle.
package dbstick

import (
	"context"
	"time"
)

// Store remembers which keys wrote recently. Every app server must share
// it, or a request that reaches another server reads from the replica.
//
// Its methods may be called at the same time from many goroutines.
//
// A call must not block for long, since a scope waits for it. Put gets a
// context that is never canceled, since the write is done. So a Store
// that talks to a server needs a timeout of its own, as go-redis has.
type Store interface {
	// Exists reports whether key was put and has not expired.
	Exists(ctx context.Context, key string) (bool, error)
	// Put stores key for ttl. A later Put replaces the expiry.
	Put(ctx context.Context, key string, ttl time.Duration) error
}

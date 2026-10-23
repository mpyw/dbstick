package dbstick

import "context"

// Router picks the primary or the replica for a context. DB is any
// database handle, such as *sql.DB or *pgxpool.Pool. Router does not wrap
// it, so every library that takes the handle works as before.
//
// Router only reads what the Tracker remembers. A write is recorded by a
// hook on the primary's driver, or by [Tracker.MarkWritten].
type Router[DB any] struct {
	tracker *Tracker
	primary DB
	replica DB
}

// NewRouter returns a Router that asks tracker which handle to use. It
// panics if tracker is nil.
func NewRouter[DB any](tracker *Tracker, primary, replica DB) *Router[DB] {
	if tracker == nil {
		panic("dbstick: NewRouter: tracker is nil")
	}
	return &Router[DB]{tracker: tracker, primary: primary, replica: replica}
}

// ForRead returns the handle to read with: the primary when
// [Tracker.IsSticky] says so, or else the replica.
func (r *Router[DB]) ForRead(ctx context.Context) DB {
	if r.tracker.IsSticky(ctx) {
		return r.primary
	}
	return r.replica
}

// ForWrite returns the primary. It records nothing: a driver hook records
// each write when it is done, such as the tracer of
// github.com/mpyw/dbstick/pgxstick.
//
// Use it for reads that must see the latest data too, such as SELECT ...
// FOR UPDATE.
func (r *Router[DB]) ForWrite() DB {
	return r.primary
}

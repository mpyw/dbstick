package dbstick

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Tracker remembers writes, within a scope and across scopes. A scope is a
// unit of work such as a request or a job, started by [Tracker.NewContext] or
// [Tracker.Middleware].
//
// Its methods may be called at the same time from many goroutines,
// including from goroutines of one scope.
type Tracker struct {
	store   Store
	key     func(ctx context.Context) string
	ttl     time.Duration                        //declscope:shared // set by options.go
	prefix  string                               //declscope:shared // set by options.go
	onError func(ctx context.Context, err error) //declscope:shared // set by options.go
	// putInterval is the least time between two Puts of a key in a scope.
	putInterval time.Duration //declscope:shared // set by options.go
	// now is replaced in tests.
	now func() time.Time
}

// trackerCtxKey finds a Tracker's scope in a context. Each Tracker has its
// own, so two Trackers do not share scopes.
type trackerCtxKey struct{ t *Tracker }

// NewTracker returns a Tracker that keeps keys in store. It panics if store
// or key is nil.
//
// key returns the key of whoever acts in ctx, such as a user ID that an
// auth middleware put there. It returns "" when there is none, such as for
// a guest. A scope without a key still reads its own writes, but its next
// one does not.
//
// key is called when a read or a write needs it, not when a scope starts.
// So an auth middleware may run before or after [Tracker.Middleware].
func NewTracker(store Store, key func(ctx context.Context) string, opts ...TrackerOption) *Tracker {
	if store == nil {
		panic("dbstick: NewTracker: store is nil")
	}
	if key == nil {
		panic("dbstick: NewTracker: key is nil")
	}
	t := &Tracker{
		store:   store,
		key:     key,
		ttl:     5 * time.Second,
		prefix:  "dbstick:",
		onError: func(context.Context, error) {},
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// TrackerOption configures a [Tracker] in [NewTracker].
type TrackerOption func(*Tracker)

// NewContext returns a context with a new scope. Reads in it go to the
// replica until it writes, or until its key is found in the Store.
//
// A request handler gets one from [Tracker.Middleware]. Call NewContext
// for other work, such as a job or a gRPC call. Outside any scope, a read
// always goes to the replica and a write is not remembered.
//
// A scope stays sticky until it ends, and looks up the Store once per
// key. So keep a scope to one short piece of work. Give a long job, or
// each message of a WebSocket connection, a scope of its own.
func (t *Tracker) NewContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, trackerCtxKey{t}, &scope{})
}

// NewStickyContext returns a context with a new scope whose reads all go
// to the primary, as if it had written. Use it for work that must read the
// latest data, such as a job that runs right after the write that queued
// it. It is ShouldAssumeModified of the Laravel package.
func (t *Tracker) NewStickyContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, trackerCtxKey{t}, &scope{sticky: true})
}

// Middleware gives each request a new scope, as [Tracker.NewContext] does.
func (t *Tracker) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(t.NewContext(r.Context())))
	})
}

// IsSticky reports whether reads in ctx should go to the primary: the
// scope is sticky, or its key wrote within the TTL. It looks up the Store
// at most once per scope and key, and only while the scope is not sticky.
//
// A lookup holds the scope's lock, so the scope's other goroutines wait
// for it.
func (t *Tracker) IsSticky(ctx context.Context) bool {
	s, ok := ctx.Value(trackerCtxKey{t}).(*scope)
	if !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sticky {
		return true
	}
	key := t.key(ctx)
	if key == "" || s.absent[key] {
		return false
	}
	found, err := t.store.Exists(ctx, t.prefix+key)
	if err != nil {
		t.onError(ctx, fmt.Errorf("dbstick: look up %q: %w", t.prefix+key, err))
		found = true
	}
	if !found {
		s.markAbsent(key)
		return false
	}
	s.sticky = true
	return true
}

// MarkWritten records a write that is done, such as one outside a
// transaction. The scope becomes sticky, and the key's later scopes read
// from the primary for the TTL.
//
// A driver hook calls it, such as the tracer of
// github.com/mpyw/dbstick/pgxstick. Call it by hand only for a database
// that has no hook.
//
// It puts the key in the Store on each call, or at most once per the
// interval of [WithPutInterval]. The Put is not canceled with ctx: the
// write is done, so it must be remembered.
func (t *Tracker) MarkWritten(ctx context.Context) {
	s, ok := ctx.Value(trackerCtxKey{t}).(*scope)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sticky = true
	t.put(ctx, s)
}

// MarkUncommitted records a write in a transaction that has not committed
// yet. The scope becomes sticky at once. The key is put in the Store only
// by [Tracker.MarkCommitted], so that the TTL starts at the commit.
func (t *Tracker) MarkUncommitted(ctx context.Context) {
	s, ok := ctx.Value(trackerCtxKey{t}).(*scope)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sticky = true
	s.uncommitted = true
}

// MarkCommitted records that a transaction committed. If the scope wrote
// in it, the key is put in the Store, as [Tracker.MarkWritten] does.
//
// The scope keeps one flag for its transactions, not one per transaction.
// So if a scope runs two at the same time and only one commits, the key
// is still put. That reads from the primary more than needed, but never
// from a stale replica.
func (t *Tracker) MarkCommitted(ctx context.Context) {
	s, ok := ctx.Value(trackerCtxKey{t}).(*scope)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.uncommitted {
		return
	}
	s.uncommitted = false
	t.put(ctx, s)
}

// MarkRolledBack records that a transaction rolled back. Its writes are
// not put in the Store. The scope stays sticky, as a scope never stops
// being sticky.
func (t *Tracker) MarkRolledBack(ctx context.Context) {
	s, ok := ctx.Value(trackerCtxKey{t}).(*scope)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uncommitted = false
}

// put puts the key of ctx in the Store, unless it was put within the
// interval of WithPutInterval. The caller holds s.mu.
func (t *Tracker) put(ctx context.Context, s *scope) {
	key := t.key(ctx)
	if key == "" {
		return
	}
	now := t.now()
	if last, ok := s.putAt[key]; ok && now.Sub(last) < t.putInterval {
		return
	}
	// A skipped write comes less than putInterval after this Put. So the
	// key must outlive the TTL by putInterval, to last the TTL after it.
	if err := t.store.Put(context.WithoutCancel(ctx), t.prefix+key, t.ttl+t.putInterval); err != nil {
		t.onError(ctx, fmt.Errorf("dbstick: put %q: %w", t.prefix+key, err))
		return
	}
	s.markPut(key, now)
}

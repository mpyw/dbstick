package dbstick

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"
)

// userKey finds the user in a context.
//
//declscope:ignore qualify // the tests' user is no part of the Tracker
type userKey struct{}

// withUser stands for an auth middleware.
//
//declscope:ignore qualify // the tests' user is no part of the Tracker
func withUser(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userKey{}, id)
}

// userID is the key function of the tests.
//
//declscope:ignore qualify // the tests' user is no part of the Tracker
func userID(ctx context.Context) string {
	id, _ := ctx.Value(userKey{}).(string) // "" when there is none
	return id
}

// newTestTracker returns a Tracker on store whose clock is *now.
func newTestTracker(store Store, now *time.Time, onError func(context.Context, error), opts ...TrackerOption) *Tracker {
	opts = append([]TrackerOption{WithTTL(10 * time.Second), WithErrorHandler(onError)}, opts...)
	t := NewTracker(store, userID, opts...)
	t.now = func() time.Time { return *now }
	return t
}

func TestNewTrackerPanics(t *testing.T) {
	for name, f := range map[string]func(){
		"nil store":             func() { NewTracker(nil, userID) },
		"nil key":               func() { NewTracker(NewMemoryStore(), nil) },
		"zero TTL":              func() { WithTTL(0) },
		"negative put interval": func() { WithPutInterval(-1) },
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

func TestNewTrackerDefaults(t *testing.T) {
	store := newFakeStore()
	tr := NewTracker(store, userID)
	ctx := withUser(tr.NewContext(context.Background()), "alice")
	tr.MarkWritten(ctx)
	if got := store.stored["dbstick:alice"]; got != 5*time.Second {
		t.Errorf("TTL of dbstick:alice = %v, want 5s", got)
	}
	// The default error handler ignores the error.
	store.err = errors.New("down")
	tr.MarkWritten(withUser(tr.NewContext(context.Background()), "bob"))
}

func TestTrackerOptions(t *testing.T) {
	store := newFakeStore()
	store.err = errors.New("down")
	tr := NewTracker(store, userID, WithPrefix(""), WithErrorHandler(nil))
	tr.MarkWritten(withUser(tr.NewContext(context.Background()), "alice"))
	if want := []string{"alice"}; !slices.Equal(store.puts, want) {
		t.Errorf("puts = %v, want %v", store.puts, want)
	}
}

func TestTrackerOutsideScope(t *testing.T) {
	store := newFakeStore()
	now := time.Now()
	tr := newTestTracker(store, &now, nil)
	ctx := withUser(context.Background(), "alice")
	tr.MarkWritten(ctx)
	if tr.IsSticky(ctx) {
		t.Error("IsSticky outside a scope = true")
	}
	if len(store.exists)+len(store.puts) != 0 {
		t.Errorf("store was called: exists %v, puts %v", store.exists, store.puts)
	}
}

func TestTrackerWithoutKey(t *testing.T) {
	store := newFakeStore()
	now := time.Now()
	tr := newTestTracker(store, &now, nil)
	ctx := tr.NewContext(context.Background())
	if tr.IsSticky(ctx) {
		t.Error("IsSticky before a write = true")
	}
	tr.MarkWritten(ctx)
	if !tr.IsSticky(ctx) {
		t.Error("IsSticky after a write = false")
	}
	if len(store.exists)+len(store.puts) != 0 {
		t.Errorf("store was called: exists %v, puts %v", store.exists, store.puts)
	}
}

func TestTrackerLooksUpOncePerKey(t *testing.T) {
	store := newFakeStore()
	now := time.Now()
	tr := newTestTracker(store, &now, nil)
	scope := tr.NewContext(context.Background())
	alice := withUser(scope, "alice")
	bob := withUser(scope, "bob")

	// Keys that are absent are looked up once each, even when they take
	// turns.
	for _, ctx := range []context.Context{alice, bob, alice, bob} {
		if tr.IsSticky(ctx) {
			t.Errorf("IsSticky(%s) = true", userID(ctx))
		}
	}
	want := []string{"dbstick:alice", "dbstick:bob"}
	if !slices.Equal(store.exists, want) {
		t.Errorf("lookups = %v, want %v", store.exists, want)
	}

	// A sticky scope needs no lookup.
	tr.MarkWritten(alice)
	if !tr.IsSticky(alice) {
		t.Error("IsSticky(alice) after a write = false")
	}
	if len(store.exists) != 2 {
		t.Errorf("lookups = %v, want no more", store.exists)
	}
}

func TestTrackerStaysStickyAfterAFoundKey(t *testing.T) {
	store := newFakeStore()
	store.stored["dbstick:bob"] = time.Second
	now := time.Now()
	tr := newTestTracker(store, &now, nil)
	scope := tr.NewContext(context.Background())
	bob := withUser(scope, "bob")

	if !tr.IsSticky(bob) {
		t.Error("IsSticky(bob) = false")
	}
	// Another key, and no key, stay on the primary without a lookup.
	for _, ctx := range []context.Context{withUser(scope, "alice"), scope} {
		if !tr.IsSticky(ctx) {
			t.Errorf("IsSticky(%q) after bob was found = false", userID(ctx))
		}
	}
	if want := []string{"dbstick:bob"}; !slices.Equal(store.exists, want) {
		t.Errorf("lookups = %v, want %v", store.exists, want)
	}
}

func TestTrackerLookupError(t *testing.T) {
	store := newFakeStore()
	store.err = errors.New("down")
	now := time.Now()
	var got []error
	tr := newTestTracker(store, &now, func(_ context.Context, err error) { got = append(got, err) })
	ctx := withUser(tr.NewContext(context.Background()), "alice")
	for range 2 {
		if !tr.IsSticky(ctx) {
			t.Error("IsSticky after a failed lookup = false, want the primary")
		}
	}
	if len(got) != 1 || !errors.Is(got[0], store.err) {
		t.Errorf("OnError got %v, want one error wrapping %v", got, store.err)
	}
}

func TestTrackerPutsOnEachWrite(t *testing.T) {
	store := newFakeStore()
	now := time.Now()
	tr := newTestTracker(store, &now, nil)
	alice := withUser(tr.NewContext(context.Background()), "alice")

	tr.MarkWritten(alice)
	tr.MarkWritten(alice)
	if len(store.puts) != 2 {
		t.Errorf("puts = %v, want two", store.puts)
	}
	if got := store.stored["dbstick:alice"]; got != 10*time.Second {
		t.Errorf("TTL of the Put = %v, want 10s", got)
	}
}

func TestTrackerPutInterval(t *testing.T) {
	store := newFakeStore()
	now := time.Now()
	tr := newTestTracker(store, &now, nil, WithPutInterval(4*time.Second))
	scope := tr.NewContext(context.Background())
	alice := withUser(scope, "alice")

	tr.MarkWritten(alice)
	// The Put outlives the TTL by the interval, so that a write it skips
	// still gets the whole TTL.
	if got := store.stored["dbstick:alice"]; got != 14*time.Second {
		t.Errorf("TTL of the Put = %v, want 14s", got)
	}
	now = now.Add(3 * time.Second)
	tr.MarkWritten(alice)
	if len(store.puts) != 1 {
		t.Errorf("puts within the interval = %v, want one", store.puts)
	}
	now = now.Add(time.Second)
	tr.MarkWritten(alice)
	if len(store.puts) != 2 {
		t.Errorf("puts after the interval = %v, want two", store.puts)
	}
	// Another key is put at once.
	tr.MarkWritten(withUser(scope, "bob"))
	want := []string{"dbstick:alice", "dbstick:alice", "dbstick:bob"}
	if !slices.Equal(store.puts, want) {
		t.Errorf("puts = %v, want %v", store.puts, want)
	}
}

func TestTrackerTransaction(t *testing.T) {
	store := newFakeStore()
	now := time.Now()
	tr := newTestTracker(store, &now, nil)
	ctx := withUser(tr.NewContext(context.Background()), "alice")

	// A commit with no write puts nothing.
	tr.MarkCommitted(ctx)
	if len(store.puts) != 0 {
		t.Errorf("puts after an empty commit = %v, want none", store.puts)
	}

	tr.MarkUncommitted(ctx)
	if !tr.IsSticky(ctx) {
		t.Error("IsSticky after an uncommitted write = false")
	}
	if len(store.puts) != 0 {
		t.Errorf("puts before the commit = %v, want none", store.puts)
	}
	tr.MarkCommitted(ctx)
	tr.MarkCommitted(ctx) // a second commit has nothing to put
	if want := []string{"dbstick:alice"}; !slices.Equal(store.puts, want) {
		t.Errorf("puts after the commit = %v, want %v", store.puts, want)
	}

	// A rollback drops the write. The scope stays sticky.
	tr.MarkUncommitted(ctx)
	tr.MarkRolledBack(ctx)
	tr.MarkCommitted(ctx)
	if len(store.puts) != 1 {
		t.Errorf("puts after a rollback = %v, want no more", store.puts)
	}
	if !tr.IsSticky(ctx) {
		t.Error("IsSticky after a rollback = false")
	}
}

func TestTrackerTransactionOutsideScope(t *testing.T) {
	store := newFakeStore()
	now := time.Now()
	tr := newTestTracker(store, &now, nil)
	ctx := withUser(context.Background(), "alice")
	tr.MarkUncommitted(ctx)
	tr.MarkCommitted(ctx)
	tr.MarkRolledBack(ctx)
	if len(store.puts) != 0 {
		t.Errorf("puts = %v, want none", store.puts)
	}
}

func TestTrackerPutError(t *testing.T) {
	store := newFakeStore()
	store.err = errors.New("down")
	now := time.Now()
	var got []error
	tr := newTestTracker(store, &now, func(_ context.Context, err error) { got = append(got, err) })
	ctx := withUser(tr.NewContext(context.Background()), "alice")
	tr.MarkWritten(ctx)
	if len(got) != 1 || !errors.Is(got[0], store.err) {
		t.Errorf("OnError got %v, want one error wrapping %v", got, store.err)
	}
	if !tr.IsSticky(ctx) {
		t.Error("IsSticky in the scope after a failed Put = false")
	}
	// A failed Put is tried again on the next write.
	store.err = nil
	tr.MarkWritten(ctx)
	if len(store.puts) != 2 {
		t.Errorf("puts = %v, want a retry", store.puts)
	}
}

func TestTrackerPutIgnoresCancel(t *testing.T) {
	store := newFakeStore()
	now := time.Now()
	tr := newTestTracker(store, &now, nil)
	ctx, cancel := context.WithCancel(withUser(tr.NewContext(context.Background()), "alice"))
	cancel()
	tr.MarkWritten(ctx)
	if err := store.putCtxs[0].Err(); err != nil {
		t.Errorf("Put's context is done: %v", err)
	}
}

func TestTrackerCarriesWritesAcrossScopes(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()
	tr := newTestTracker(store, &now, nil)
	tr.MarkWritten(withUser(tr.NewContext(context.Background()), "alice"))
	if !tr.IsSticky(withUser(tr.NewContext(context.Background()), "alice")) {
		t.Error("IsSticky in alice's next scope = false")
	}
	if tr.IsSticky(withUser(tr.NewContext(context.Background()), "bob")) {
		t.Error("IsSticky in bob's scope = true")
	}
}

func TestTrackerNewStickyContext(t *testing.T) {
	store := newFakeStore()
	now := time.Now()
	tr := newTestTracker(store, &now, nil)
	ctx := withUser(tr.NewStickyContext(context.Background()), "alice")
	if !tr.IsSticky(ctx) {
		t.Error("IsSticky = false")
	}
	if len(store.exists) != 0 {
		t.Errorf("lookups = %v, want none", store.exists)
	}
}

func TestTrackerScopesAreTheirOwn(t *testing.T) {
	now := time.Now()
	a := newTestTracker(newFakeStore(), &now, nil)
	b := newTestTracker(newFakeStore(), &now, nil)
	ctx := b.NewContext(a.NewContext(context.Background()))
	a.MarkWritten(ctx)
	if b.IsSticky(ctx) {
		t.Error("a write of one Tracker made another sticky")
	}
}

func TestTrackerMiddleware(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()
	tr := newTestTracker(store, &now, nil)
	var sticky []bool
	h := tr.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx := withUser(r.Context(), "alice")
		sticky = append(sticky, tr.IsSticky(ctx))
		if r.Method == http.MethodPost {
			tr.MarkWritten(ctx)
		}
	}))
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodGet} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/", nil))
	}
	if sticky[0] || !sticky[2] {
		t.Errorf("IsSticky per request = %v, want [false _ true]", sticky)
	}
}

func TestTrackerConcurrentScope(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()
	tr := newTestTracker(store, &now, nil)
	ctx := withUser(tr.NewContext(context.Background()), "alice")
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			if i%2 == 0 {
				tr.MarkWritten(ctx)
			}
			_ = tr.IsSticky(ctx) // only the race detector checks this
		})
	}
	wg.Wait()
	if !tr.IsSticky(ctx) {
		t.Error("IsSticky after concurrent writes = false")
	}
}

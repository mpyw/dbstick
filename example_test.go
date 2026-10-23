package dbstick_test

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/mpyw/dbstick"
)

type userKey struct{}

// withUser stands for an auth middleware.
func withUser(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userKey{}, id)
}

func userID(ctx context.Context) string {
	id, _ := ctx.Value(userKey{}).(string) // "" for a guest
	return id
}

func ExampleRouter() {
	tracker := dbstick.NewTracker(dbstick.NewMemoryStore(), userID)
	db := dbstick.NewRouter(tracker, "primary", "replica")

	ctx := withUser(tracker.NewContext(context.Background()), "alice")
	fmt.Println("before a write:", db.ForRead(ctx))
	_ = db.ForWrite()        // INSERT ...
	tracker.MarkWritten(ctx) // a driver hook does this
	fmt.Println("after a write:", db.ForRead(ctx))

	next := withUser(tracker.NewContext(context.Background()), "alice")
	fmt.Println("alice's next request:", db.ForRead(next))

	other := withUser(tracker.NewContext(context.Background()), "bob")
	fmt.Println("bob's request:", db.ForRead(other))
	// Output:
	// before a write: replica
	// after a write: primary
	// alice's next request: primary
	// bob's request: replica
}

// A Router holds any handle. With database/sql:
func ExampleRouter_databaseSQL() {
	primary, _ := sql.Open("pgx", "postgres://primary/app") // errors elided
	replica, _ := sql.Open("pgx", "postgres://replica/app")
	tracker := dbstick.NewTracker(dbstick.NewMemoryStore(), userID)
	db := dbstick.NewRouter(tracker, primary, replica)

	http.HandleFunc("POST /posts", func(w http.ResponseWriter, r *http.Request) {
		// The primary's driver hook, such as pgxstick, records the write.
		_, err := db.ForWrite().ExecContext(r.Context(), "INSERT INTO posts ...")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/posts", http.StatusSeeOther)
	})
	http.HandleFunc("GET /posts", func(w http.ResponseWriter, r *http.Request) {
		// Within the TTL of the POST, this reads from the primary.
		rows, err := db.ForRead(r.Context()).QueryContext(r.Context(), "SELECT ...")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = rows.Close() }() // a read has nothing to lose
	})
	_ = http.ListenAndServe(":8080", tracker.Middleware(http.DefaultServeMux)) // errors elided
}

func ExampleTracker_Middleware() {
	tracker := dbstick.NewTracker(dbstick.NewMemoryStore(), userID)
	db := dbstick.NewRouter(tracker, "primary", "replica")

	h := tracker.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx := withUser(r.Context(), "alice")
		if r.Method == http.MethodPost {
			_ = db.ForWrite()
			tracker.MarkWritten(ctx)
		}
		fmt.Println(r.Method, "reads from", db.ForRead(ctx))
	}))
	for _, method := range []string{"GET", "POST", "GET"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/", nil))
	}
	// Output:
	// GET reads from replica
	// POST reads from primary
	// GET reads from primary
}

func ExampleNewTracker() {
	tracker := dbstick.NewTracker(
		dbstick.NewMemoryStore(),
		userID,
		dbstick.WithTTL(10*time.Second),
		dbstick.WithPrefix("main:"),
		dbstick.WithErrorHandler(func(ctx context.Context, err error) {
			slog.WarnContext(ctx, "dbstick", "err", err)
		}),
	)
	db := dbstick.NewRouter(tracker, "primary", "replica")

	ctx := withUser(tracker.NewContext(context.Background()), "alice")
	fmt.Println(db.ForRead(ctx))
	// Output:
	// replica
}

func ExampleTracker_NewStickyContext() {
	tracker := dbstick.NewTracker(dbstick.NewMemoryStore(), userID)
	db := dbstick.NewRouter(tracker, "primary", "replica")

	job := tracker.NewStickyContext(context.Background())
	fmt.Println(db.ForRead(job))
	// Output:
	// primary
}

func ExampleRouter_ForWrite() {
	tracker := dbstick.NewTracker(dbstick.NewMemoryStore(), userID)
	db := dbstick.NewRouter(tracker, "primary", "replica")

	// ForWrite records nothing. So a read that must see the latest data,
	// such as SELECT ... FOR UPDATE, may use it too.
	ctx := withUser(tracker.NewContext(context.Background()), "alice")
	fmt.Println(db.ForWrite(), db.ForRead(ctx))
	// Output:
	// primary replica
}

func ExampleTracker_MarkUncommitted() {
	tracker := dbstick.NewTracker(dbstick.NewMemoryStore(), userID)
	db := dbstick.NewRouter(tracker, "primary", "replica")

	// A driver hook calls these, around a transaction.
	ctx := withUser(tracker.NewContext(context.Background()), "alice")
	tracker.MarkUncommitted(ctx) // INSERT ... in the transaction
	fmt.Println("in this request:", db.ForRead(ctx))

	next := withUser(tracker.NewContext(context.Background()), "alice")
	fmt.Println("next, before the commit:", db.ForRead(next))

	tracker.MarkCommitted(ctx) // COMMIT
	next = withUser(tracker.NewContext(context.Background()), "alice")
	fmt.Println("next, after the commit:", db.ForRead(next))
	// Output:
	// in this request: primary
	// next, before the commit: replica
	// next, after the commit: primary
}

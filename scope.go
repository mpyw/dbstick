package dbstick

import (
	"sync"
	"time"
)

// scope is the state of one scope: a request, a job, or other work that
// a Tracker started.
//
//declscope:shared // tracker.go reads and changes it
type scope struct {
	mu sync.Mutex
	// sticky sends the rest of the scope's reads to the primary. It is set
	// by a write, by NewStickyContext, or by finding the key in the Store,
	// and never unset. So a scope that switches keys stays on the primary,
	// as a request does in the Laravel package.
	sticky bool
	// uncommitted is set by a write in a transaction, and cleared when the
	// transaction ends. A commit then puts the key in the Store.
	uncommitted bool
	// absent and putAt record the Store calls, per key. The key is read on
	// each call, so it may change within a scope, such as on a login.
	//
	// absent holds the keys that a lookup did not find, so that they are
	// not looked up again. A lookup that finds its key sets sticky instead.
	absent map[string]bool
	// putAt holds the time of the last successful Put of each key.
	putAt map[string]time.Time
}

// markAbsent records that a lookup did not find key. The caller holds
// s.mu.
//
//declscope:shared // tracker.go records each lookup
func (s *scope) markAbsent(key string) {
	if s.absent == nil {
		s.absent = map[string]bool{}
	}
	s.absent[key] = true
}

// markPut records that a Put of key succeeded at t. The caller holds
// s.mu.
//
//declscope:shared // tracker.go records each Put
func (s *scope) markPut(key string, t time.Time) {
	if s.putAt == nil {
		s.putAt = map[string]time.Time{}
	}
	s.putAt[key] = t
}

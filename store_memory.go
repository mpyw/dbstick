//declscope:namespace memoryStore

package dbstick

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is a [Store] in the memory of one process. It suits tests,
// and a single app server. With several servers, use a shared Store such
// as the one in github.com/mpyw/dbstick/redisstore.
type MemoryStore struct {
	mu      sync.Mutex
	expires map[string]time.Time
	// sweepAt is the size at which Put next drops expired keys.
	sweepAt int
	// now is replaced in tests.
	now func() time.Time
}

// memoryStoreMinSweep is the smallest size at which Put drops expired keys.
const memoryStoreMinSweep = 64

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		expires: map[string]time.Time{},
		sweepAt: memoryStoreMinSweep,
		now:     time.Now,
	}
}

// Exists reports whether key was put and has not expired. It never fails.
func (s *MemoryStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.expires[key]
	if !ok {
		return false, nil
	}
	if !s.now().Before(expiry) {
		delete(s.expires, key)
		return false, nil
	}
	return true, nil
}

// Put stores key for ttl. It never fails. Once the store has grown to
// twice its size after the last sweep, Put drops every expired key, so
// keys that are never read again do not pile up.
func (s *MemoryStore) Put(_ context.Context, key string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.expires[key] = now.Add(ttl)
	if len(s.expires) < s.sweepAt {
		return nil
	}
	for k, expiry := range s.expires {
		if !now.Before(expiry) {
			delete(s.expires, k)
		}
	}
	s.sweepAt = max(2*len(s.expires), memoryStoreMinSweep)
	return nil
}

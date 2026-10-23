package dbstick

import (
	"context"
	"sync"
	"time"
)

// fakeStore counts calls and can fail them.
//
//declscope:shared // tracker_test.go tests with it
type fakeStore struct {
	stored  map[string]time.Duration
	exists  []string
	puts    []string
	putCtxs []context.Context
	err     error
	// mu guards the fields above.
	//
	//declscope:private // only the methods lock it
	mu sync.Mutex
}

//declscope:shared // tracker_test.go tests with it
func newFakeStore() *fakeStore {
	return &fakeStore{stored: map[string]time.Duration{}}
}

func (s *fakeStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exists = append(s.exists, key)
	if s.err != nil {
		return false, s.err
	}
	_, ok := s.stored[key]
	return ok, nil
}

func (s *fakeStore) Put(ctx context.Context, key string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts = append(s.puts, key)
	s.putCtxs = append(s.putCtxs, ctx)
	if s.err != nil {
		return s.err
	}
	s.stored[key] = ttl
	return nil
}

//declscope:namespace memoryStore // tests store_memory.go

package dbstick

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func newTestMemoryStore(now *time.Time) *MemoryStore {
	s := NewMemoryStore()
	s.now = func() time.Time { return *now }
	return s
}

func TestMemoryStoreExpires(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s := newTestMemoryStore(&now)
	if err := s.Put(ctx, "alice", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		after time.Duration
		want  bool
	}{
		{0, true},
		{9 * time.Second, true},
		{10 * time.Second, false},
	} {
		at := now
		now = now.Add(tc.after)
		got, err := s.Exists(ctx, "alice")
		now = at
		if err != nil || got != tc.want {
			t.Errorf("Exists after %v = %v, %v, want %v", tc.after, got, err, tc.want)
		}
	}
	if _, ok := s.expires["alice"]; ok {
		t.Error("an expired key was kept after Exists")
	}
	if got, err := s.Exists(ctx, "bob"); err != nil || got {
		t.Errorf("Exists(bob) = %v, %v, want false", got, err)
	}
}

func TestMemoryStorePutReplacesExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s := newTestMemoryStore(&now)
	for _, ttl := range []time.Duration{10 * time.Second, time.Second} {
		if err := s.Put(ctx, "alice", ttl); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Second)
	if got, _ := s.Exists(ctx, "alice"); got { // MemoryStore never fails
		t.Error("Exists = true, want the later Put's expiry")
	}
}

func TestMemoryStoreSweeps(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s := newTestMemoryStore(&now)
	for i := range memoryStoreMinSweep - 1 {
		if err := s.Put(ctx, fmt.Sprint("old", i), time.Second); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(time.Second)
	if err := s.Put(ctx, "new", time.Second); err != nil {
		t.Fatal(err)
	}
	if len(s.expires) != 1 {
		t.Errorf("keys after a sweep = %d, want 1", len(s.expires))
	}
	if s.sweepAt != memoryStoreMinSweep {
		t.Errorf("sweepAt = %d, want %d", s.sweepAt, memoryStoreMinSweep)
	}
}

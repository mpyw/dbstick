package redisstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mpyw/dbstick/redisstore"
)

// The clients of go-redis meet Client.
var (
	_ redisstore.Client = (*redis.Client)(nil)
	_ redisstore.Client = (*redis.ClusterClient)(nil)
	_ redisstore.Client = (*redis.Ring)(nil)
)

func TestStore(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	s := redisstore.New(redis.NewClient(&redis.Options{Addr: mr.Addr()}))

	if got, err := s.Exists(ctx, "alice"); err != nil || got {
		t.Errorf("Exists before Put = %v, %v, want false", got, err)
	}
	if err := s.Put(ctx, "alice", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Exists(ctx, "alice"); err != nil || !got {
		t.Errorf("Exists after Put = %v, %v, want true", got, err)
	}
	if ttl := mr.TTL("alice"); ttl != 10*time.Second {
		t.Errorf("TTL = %v, want 10s", ttl)
	}
	mr.FastForward(10 * time.Second)
	if got, err := s.Exists(ctx, "alice"); err != nil || got {
		t.Errorf("Exists after the TTL = %v, %v, want false", got, err)
	}
}

func TestStoreErrors(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	s := redisstore.New(redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1}))
	mr.Close()

	if _, err := s.Exists(ctx, "alice"); err == nil {
		t.Error("Exists with Redis down: no error")
	}
	if err := s.Put(ctx, "alice", time.Second); err == nil {
		t.Error("Put with Redis down: no error")
	}
}

// Package redisstore is a [github.com/mpyw/dbstick.Store] in Redis, for
// app servers that share one Redis.
//
// It is a module of its own, so that dbstick itself needs no Redis client.
// It does not import dbstick either: Store is an interface, and this type
// meets it.
package redisstore

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client is the part of a go-redis client that Store calls.
// *redis.Client, *redis.ClusterClient and *redis.Ring all meet it.
type Client interface {
	Exists(ctx context.Context, keys ...string) *redis.IntCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
}

// Store keeps each key as a Redis key that expires with its TTL.
type Store struct {
	client Client
}

// New returns a Store on client.
func New(client Client) *Store {
	return &Store{client: client}
}

// Exists reports whether key is in Redis. Redis drops it when it expires.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	n, err := s.client.Exists(ctx, key).Result()
	return n > 0, err
}

// Put sets key in Redis to expire after ttl. Redis keeps the TTL in
// milliseconds, so ttl is rounded down to them.
func (s *Store) Put(ctx context.Context, key string, ttl time.Duration) error {
	return s.client.Set(ctx, key, 1, ttl).Err()
}

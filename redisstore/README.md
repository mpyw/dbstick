[← dbstick](../README.md)

# redisstore

A [dbstick](../README.md) Store for [go-redis](https://github.com/redis/go-redis). It keeps the keys of the users who wrote in Redis.

Use it when several servers share a Redis.

## Install

```sh
go get github.com/mpyw/dbstick/redisstore
```

## Usage

```go
rdb := redis.NewClient(&redis.Options{
	Addr: "localhost:6379",
})
tracker := dbstick.NewTracker(
	redisstore.New(rdb),
	auth.UserID,
)
```

## How it works

Each key is a Redis key that expires with its TTL. A lookup is `EXISTS`, and a write is `SET` with the TTL.

## Notes

- go-redis has timeouts of its own, so a call never blocks for long. Set them in `redis.Options` if the defaults do not fit.

## Interface

`New` takes a `Client`, which needs only `Exists` and `Set`. `*redis.Client`, `*redis.ClusterClient` and `*redis.Ring` implement it. In a test, a fake can return go-redis's result types:

```go
func (fakeClient) Exists(
	ctx context.Context,
	keys ...string,
) *redis.IntCmd {
	return redis.NewIntResult(1, nil)
}
```

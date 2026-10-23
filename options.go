//declscope:namespace with // every option is With*, Go's option idiom

package dbstick

import (
	"context"
	"fmt"
	"time"
)

// WithTTL sets how long reads stay on the primary after a write. It should
// be longer than the replica's lag. The default is 5 seconds, as in the
// Laravel package. It panics if ttl is not positive.
func WithTTL(ttl time.Duration) TrackerOption {
	if ttl <= 0 {
		panic(fmt.Sprintf("dbstick: WithTTL: ttl %v is not positive", ttl))
	}
	return func(t *Tracker) { t.ttl = ttl }
}

// WithPrefix sets what starts every key in the Store. Two Trackers that
// share a Store, such as one per database cluster, need different
// prefixes. The default is "dbstick:".
func WithPrefix(prefix string) TrackerOption {
	return func(t *Tracker) { t.prefix = prefix }
}

// WithErrorHandler sets the function that receives each error of the
// Store. A read whose lookup fails goes to the primary, and a write whose
// Put fails is not remembered beyond its scope. The default, and nil,
// ignore the errors.
func WithErrorHandler(f func(ctx context.Context, err error)) TrackerOption {
	return func(t *Tracker) {
		if f == nil {
			f = func(context.Context, error) {}
		}
		t.onError = f
	}
}

// WithPutInterval puts a key in the Store at most once per interval, within
// one scope. It saves calls to the Store when a scope writes many times.
// The default, 0, puts on every write. It panics if interval is negative.
//
// A Put then keeps the key for the TTL plus interval. So reads still stay
// on the primary for at least the TTL after the last write, and for at
// most interval longer.
func WithPutInterval(interval time.Duration) TrackerOption {
	if interval < 0 {
		panic(fmt.Sprintf("dbstick: WithPutInterval: interval %v is negative", interval))
	}
	return func(t *Tracker) { t.putInterval = interval }
}

// Lock helpers built on the CacheBackend interface. Mirrors TS
// `cacheService.lock(scope, key, seconds, abortOnHold)` — used by capital +
// identity flows where two concurrent requests for the same (scope, key)
// must serialise.
package services

import (
	"errors"
	"time"
)

// ErrLockHeld is returned by AcquireLock when abortOnHold=true and the lock
// is currently held by another caller.
var ErrLockHeld = errors.New("lock held by another caller")

// AcquireLock attempts to set `scope:key` in the cache with the supplied
// expiry. Returns ok=true when this caller is the lock-holder; false when
// the slot was already held. abortOnHold=true folds the (ok=false, nil) case
// into an ErrLockHeld error so callers can short-circuit with `return err`.
//
// The returned `release` closure deletes the cache entry; idempotent —
// calling release on a never-acquired lock is a no-op.
func AcquireLock(cache CacheBackend, scope, key string, expiry time.Duration, abortOnHold bool) (release func(), err error) {
	if cache == nil || scope == "" || key == "" {
		return func() {}, nil
	}
	if expiry <= 0 {
		expiry = 10 * time.Second
	}
	cacheKey := lockKey(scope, key)
	ok := cache.Setnx(cacheKey, "1", expiry)
	if !ok {
		if abortOnHold {
			return func() {}, ErrLockHeld
		}
		return func() {}, nil
	}
	released := false
	return func() {
		if released || cache == nil {
			return
		}
		released = true
		cache.Del(cacheKey)
	}, nil
}

// lockKey returns the cache key for a (scope, key) pair. Kept stable so
// independent processes / pods coordinate on the same slot.
func lockKey(scope, key string) string {
	return "packages.core.lock:" + scope + ":" + key
}

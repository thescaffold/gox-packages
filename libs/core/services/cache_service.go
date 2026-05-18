package services

import (
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

// CacheService is the two-tier cache mirroring TS
// ntx-packages/libs/core/src/services/cache.service.ts. It composes an
// in-memory L1 (always present) with an optional L2 backed by the platform
// `apps.cache.*` HTTP API. Reads consult L1 first; on miss, the L2 value is
// fetched and promoted to L1. Writes update both tiers (best-effort: L2
// failures are swallowed because the TS service does the same).
//
// CacheService implements CacheBackend so it can be dropped in anywhere a
// MemoryBackend is currently used.
type CacheService struct {
	l1 *MemoryBackend
	l2 L2

	cleanupOnce sync.Once
	cleanupStop chan struct{}
}

// L2 is the optional secondary tier. Implementations include the platform-
// backed adapter; pass nil to disable L2 entirely (memory-only).
//
// Methods mirror CacheBackend so the same contract holds across both tiers,
// but L2 implementations are expected to be best-effort — they MUST NOT
// panic on transient failures.
type L2 interface {
	Get(key string) (string, bool)
	Set(key, value string, ttl time.Duration)
	Del(key string)
	Setnx(key, value string, ttl time.Duration) bool
	Getset(key, value string, ttl time.Duration) string
	Incr(key string) int64
	TTL(key string) time.Duration
}

// NewCacheService builds a Service with a fresh in-memory L1 and the given
// optional L2. Pass nil for memory-only operation (matches today's
// MemoryBackend behaviour).
func NewCacheService(l2 L2) *CacheService {
	return &CacheService{l1: NewMemoryBackend(), l2: l2}
}

// NewCacheServiceWith allows tests / advanced callers to supply a
// pre-populated L1.
func NewCacheServiceWith(l1 *MemoryBackend, l2 L2) *CacheService {
	if l1 == nil {
		l1 = NewMemoryBackend()
	}
	return &CacheService{l1: l1, l2: l2}
}

// Get returns the value for key, consulting L1 then L2. A successful L2 hit
// is promoted to L1 with the remaining TTL.
func (s *CacheService) Get(key string) (string, bool) {
	if v, ok := s.l1.Get(key); ok {
		return v, true
	}
	if s.l2 == nil {
		return "", false
	}
	v, ok := s.l2.Get(key)
	if !ok {
		return "", false
	}
	// Promote to L1 with remaining TTL when known; otherwise no expiry.
	if ttl := s.l2.TTL(key); ttl > 0 {
		s.l1.Set(key, v, ttl)
	} else {
		s.l1.Set(key, v, 0)
	}
	return v, true
}

// Set writes to both tiers. ttl=0 means no expiry (L1) and forwards to L2.
func (s *CacheService) Set(key, value string, ttl time.Duration) {
	s.l1.Set(key, value, ttl)
	if s.l2 != nil {
		s.l2.Set(key, value, ttl)
	}
}

// Del removes from both tiers.
func (s *CacheService) Del(key string) {
	s.l1.Del(key)
	if s.l2 != nil {
		s.l2.Del(key)
	}
}

// Setnx returns true only when the key was acquired in the AUTHORITATIVE tier.
// When L2 is present, L2 is the source of truth; L1 mirrors the outcome.
// When L2 is absent, L1 alone decides.
func (s *CacheService) Setnx(key, value string, ttl time.Duration) bool {
	if s.l2 != nil {
		ok := s.l2.Setnx(key, value, ttl)
		if ok {
			s.l1.Set(key, value, ttl)
		}
		return ok
	}
	return s.l1.Setnx(key, value, ttl)
}

// Getset atomically swaps the value and returns the previous one.
// L2 (when present) is the authoritative tier; L1 is updated to the new value.
func (s *CacheService) Getset(key, value string, ttl time.Duration) string {
	if s.l2 != nil {
		prev := s.l2.Getset(key, value, ttl)
		s.l1.Set(key, value, ttl)
		return prev
	}
	return s.l1.Getset(key, value, ttl)
}

// Incr atomically increments and returns the new value. Authoritative on L2
// when present.
func (s *CacheService) Incr(key string) int64 {
	if s.l2 != nil {
		v := s.l2.Incr(key)
		// Mirror the new value into L1 with no expiry — matches TS which sets
		// `expiresAt = new Date(MAX_TIMESTAMP)` for incr.
		s.l1.Set(key, formatInt(v), 0)
		return v
	}
	return s.l1.Incr(key)
}

// TTL returns the remaining lifetime. L1 takes precedence when it has the key.
func (s *CacheService) TTL(key string) time.Duration {
	if d := s.l1.TTL(key); d > 0 {
		return d
	}
	if s.l2 != nil {
		return s.l2.TTL(key)
	}
	return 0
}

// Cache is the higher-level get-or-compute helper. Returns the cached value
// when present; otherwise invokes fn(), stores the result with ttl, and
// returns it. Mirrors TS CacheService.cache(key, fn, ttl).
func (s *CacheService) Cache(key string, fn func() (string, error), ttl time.Duration) (string, error) {
	if v, ok := s.Get(key); ok {
		return v, nil
	}
	v, err := fn()
	if err != nil {
		return "", err
	}
	s.Set(key, v, ttl)
	return v, nil
}

// Lock acquires a named lock for at most expiry. When wait=false, returns
// errLockBusy immediately if the lock is held; when wait=true, retries with
// jittered 500ms-1500ms back-off until either acquired or expiry+1s elapsed
// (matching TS sleep(500 + Math.random()*1000) + (Date.now()-start)/1000 > expiry+1).
func (s *CacheService) Lock(typ, ref string, expiry time.Duration, wait bool) error {
	start := time.Now()
	key := "package:core:lock:" + typ + ":" + ref
	for {
		if s.Setnx(key, "1", expiry) {
			return nil
		}
		if !wait {
			return errLockBusy
		}
		// jittered back-off
		time.Sleep(time.Duration(500+rand.IntN(1000)) * time.Millisecond)
		if time.Since(start) > expiry+time.Second {
			return errLockBusy
		}
	}
}

// Unlock releases a previously acquired lock. Idempotent.
func (s *CacheService) Unlock(typ, ref string) {
	s.Del("package:core:lock:" + typ + ":" + ref)
}

// errLockBusy mirrors TS error('System', 'Try again in a moment').
var errLockBusy = errors.New("lock busy: try again in a moment")

// StartCleanup launches a goroutine that periodically purges expired L1
// entries, mirroring TS CacheService.onModuleInit's `setInterval(cleanup,
// 60_000)`. Safe to call multiple times — only the first invocation starts
// the goroutine.
func (s *CacheService) StartCleanup(interval time.Duration) {
	s.cleanupOnce.Do(func() {
		s.cleanupStop = make(chan struct{})
		if interval <= 0 {
			interval = 60 * time.Second
		}
		go func() {
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-s.cleanupStop:
					return
				case <-t.C:
					s.l1.purgeExpired()
				}
			}
		}()
	})
}

// StopCleanup stops the cleanup goroutine if running.
func (s *CacheService) StopCleanup() {
	if s.cleanupStop != nil {
		close(s.cleanupStop)
		s.cleanupStop = nil
	}
}

// Package sync ports ntx-packages/libs/core/src/services/sync.service.ts.
// SyncService.Once ensures a function runs at most once across instances of
// the app, using a CacheBackend as the distributed lock.
package sync

import (
	"strconv"
	"time"

	"github.com/thescaffold/gox-packages-core/services"
)

// Service runs a function exactly once across instances using a cache lock.
type Service struct {
	cache services.CacheBackend
}

// New constructs a SyncService backed by the given cache.
func New(cache services.CacheBackend) *Service { return &Service{cache: cache} }

// Once runs fn at most once for the given key.
//
//	timeout — a generous upper bound on fn's runtime; another instance may take
//	          over after this elapses.
//	release — when true, the lock is deleted after fn completes (others can run).
//	repeat  — when false, a "done" marker is set so subsequent calls are skipped.
//
// Mirrors TS SyncService.once() semantics line-for-line.
func (s *Service) Once(key string, fn func() error, timeout time.Duration, release, repeat bool) error {
	doneKey := key + ":done"

	// Skip if already done and not allowed to repeat.
	if !repeat {
		if v, ok := s.cache.Get(doneKey); ok && v == "1" {
			return nil
		}
	}

	now := time.Now().UnixMilli()
	nowStr := strconv.FormatInt(now, 10)

	if !s.cache.Setnx(key, nowStr, timeout) {
		// Lock exists — check if it's outdated.
		lockTS, ok := s.cache.Get(key)
		if !ok {
			return nil
		}
		lock, _ := strconv.ParseInt(lockTS, 10, 64)
		if now-lock <= int64(timeout/time.Millisecond) {
			// still valid, skip
			return nil
		}
		// Lock outdated — try to take over.
		old := s.cache.Getset(key, nowStr, timeout)
		if old != lockTS {
			// someone else took over already
			return nil
		}
	}

	if err := fn(); err != nil {
		// On error, release lock anyway and propagate.
		if release {
			s.cache.Del(key)
		}
		return err
	}

	if release {
		s.cache.Del(key)
	}
	if !repeat {
		s.cache.Set(doneKey, "1", 0)
	}
	return nil
}

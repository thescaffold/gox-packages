// Package throttler ports ntx-packages/libs/core/src/services/throttler.service.ts.
// ThrottlerService rate-limits per-id activity using a cache-based counter.
package throttler

import (
	"time"

	"github.com/thescaffold/gox-packages/libs/core/services"
)

// Service applies a sliding-window rate limit to an id key.
type Service struct {
	cache services.CacheBackend
	// TTL is the rolling window duration.
	TTL time.Duration
	// Limit is the maximum allowed hits per window.
	Limit int
}

// New constructs a ThrottlerService.
func New(cache services.CacheBackend, ttl time.Duration, limit int) *Service {
	if ttl <= 0 {
		ttl = time.Minute
	}
	if limit <= 0 {
		limit = 60
	}
	return &Service{cache: cache, TTL: ttl, Limit: limit}
}

// Throttle records a hit for id and returns (allowed, remaining-ttl).
// Mirrors TS ThrottlerService.throttle().
// Throttle counts one request for id and reports whether it is within the
// limit, with the time left in the window. The count is taken atomically
// (Setnx opens the window, Incr counts within it) and compared afterwards, so
// concurrent requests cannot slip past the limit.
func (s *Service) Throttle(id string, multiplier int) (bool, time.Duration) {
	if multiplier <= 0 {
		multiplier = 1
	}
	key := "app:user:throttle:" + id

	var count int64 = 1
	if !s.cache.Setnx(key, "1", s.TTL) {
		count = s.cache.Incr(key)
	}
	rTTL := s.cache.TTL(key)
	return count <= int64(s.Limit*multiplier), rTTL
}

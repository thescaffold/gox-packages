// Package throttler ports ntx-packages/libs/core/src/services/throttler.service.ts.
// ThrottlerService rate-limits per-id activity using a cache-based counter.
package throttler

import (
	"time"

	"github.com/thescaffold/gox-packages-core/services"
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
func (s *Service) Throttle(id string, multiplier int) (bool, time.Duration) {
	if multiplier <= 0 {
		multiplier = 1
	}
	key := "app:user:throttle:" + id

	rTTL := s.cache.TTL(key)
	current, has := s.cache.Get(key)
	if has {
		count := atoi(current)
		if count >= s.Limit*multiplier {
			return false, rTTL
		}
		s.cache.Incr(key)
		return true, rTTL
	}
	s.cache.Set(key, "1", s.TTL)
	return true, s.TTL
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

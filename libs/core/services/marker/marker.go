// Package marker ports ntx-packages/libs/core/src/services/marker.service.ts.
// MarkerService stores opaque key→value markers in cache for short-lived
// idempotency / one-time-link checks.
package marker

import (
	"time"

	"github.com/thescaffold/gox-packages-core/services"
)

const baseKey = "packages.core.marker"

// Service stores and verifies cached markers.
type Service struct {
	cache services.CacheBackend
}

// New constructs a MarkerService backed by the given cache.
func New(cache services.CacheBackend) *Service { return &Service{cache: cache} }

// Mark stores a value under key for the given expiry. Defaults to 7 minutes
// when expiry is zero (matching the TS default of 7 * 60 * 1000 ms).
func (s *Service) Mark(key, value string, expiry time.Duration) {
	if expiry <= 0 {
		expiry = 7 * time.Minute
	}
	s.cache.Set(baseKey+":"+key, value, expiry)
}

// Verify reports whether the marker at key matches value. When clear is true,
// the marker is deleted on a successful verify (single-use semantics).
func (s *Service) Verify(key, value string, clear bool) bool {
	existing, ok := s.cache.Get(baseKey + ":" + key)
	if !ok || existing != value {
		return false
	}
	if clear {
		s.cache.Del(baseKey + ":" + key)
	}
	return true
}

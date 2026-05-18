package platform

import (
	"fmt"
	"time"

	"github.com/thescaffold/gox-packages/libs/core/services"
)

// CacheL2 adapts a *platform.Service to the services.L2 interface used by
// services.CacheService. All operations are best-effort: HTTP failures
// degrade silently to L1-only behaviour, matching TS CacheService which
// `await this.platformService.apps.cache.*` and ignores rejections via the
// `[, , data]` destructuring (data is undefined on error).
type CacheL2 struct {
	Service *Service
}

// NewCacheL2 returns an L2 backed by the given PlatformService.
func NewCacheL2(svc *Service) *CacheL2 {
	return &CacheL2{Service: svc}
}

// Compile-time guarantee that CacheL2 satisfies services.L2.
var _ services.L2 = (*CacheL2)(nil)

func (p *CacheL2) Get(key string) (string, bool) {
	if p == nil || p.Service == nil {
		return "", false
	}
	res := p.Service.CacheGet(key)
	if !res.Status {
		return "", false
	}
	if m, ok := res.Data.(map[string]any); ok {
		if v, ok := m["value"].(string); ok {
			return v, true
		}
	}
	return "", false
}

func (p *CacheL2) Set(key, value string, ttl time.Duration) {
	if p == nil || p.Service == nil {
		return
	}
	p.Service.CacheSet(key, value, durationSeconds(ttl))
}

func (p *CacheL2) Del(key string) {
	if p == nil || p.Service == nil {
		return
	}
	p.Service.CacheDel(key)
}

func (p *CacheL2) Setnx(key, value string, ttl time.Duration) bool {
	if p == nil || p.Service == nil {
		return false
	}
	res := p.Service.CacheSetnx(key, value, durationSeconds(ttl))
	if !res.Status {
		return false
	}
	// TS apps.cache.setnx returns 0/1 (number); coerce both numeric and bool shapes.
	switch v := res.Data.(type) {
	case bool:
		return v
	case float64:
		return v == 1
	case int:
		return v == 1
	}
	return false
}

func (p *CacheL2) Getset(key, value string, ttl time.Duration) string {
	if p == nil || p.Service == nil {
		return ""
	}
	res := p.Service.CacheGetset(key, value, durationSeconds(ttl))
	if !res.Status {
		return ""
	}
	if m, ok := res.Data.(map[string]any); ok {
		if v, ok := m["value"].(string); ok {
			return v
		}
	}
	if s, ok := res.Data.(string); ok {
		return s
	}
	return ""
}

func (p *CacheL2) Incr(key string) int64 {
	if p == nil || p.Service == nil {
		return 0
	}
	res := p.Service.CacheIncr(key)
	if !res.Status {
		return 0
	}
	switch v := res.Data.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		var n int64
		_, _ = fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}

func (p *CacheL2) TTL(key string) time.Duration {
	if p == nil || p.Service == nil {
		return 0
	}
	res := p.Service.CacheTTL(key)
	if !res.Status {
		return 0
	}
	var seconds float64
	switch v := res.Data.(type) {
	case float64:
		seconds = v
	case int:
		seconds = float64(v)
	case int64:
		seconds = float64(v)
	}
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// durationSeconds converts a time.Duration to whole seconds for the platform
// API. Sub-second values round up to 1; zero stays zero (no expiry).
func durationSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	if d < time.Second {
		return 1
	}
	return int(d / time.Second)
}

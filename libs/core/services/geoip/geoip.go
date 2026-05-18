// Package geoip ports ntx-packages/libs/core/src/services/geo-ip.service.ts.
//
// The TS service uses geoip-lite which embeds a MaxMind binary database. core
// stays dependency-free, so this package defines the Provider contract plus a
// few ready-to-wire implementations:
//
//   - MapProvider:  in-memory lookup table (tests, fixtures, ENV-loaded data)
//   - HTTPProvider: fan-out to a public geoip HTTP API (ip-api.com style)
//   - NoopProvider: always returns nil (default / opt-out)
//
// Apps that need bit-exact MaxMind output can implement their own Provider on
// top of github.com/oschwald/maxminddb-golang + their licensed MMDB file —
// keep that dependency out of core.
package geoip

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Result mirrors the TS GeoIPService.lookup() shape. Pointer fields encode
// "field absent" the same way TS does — a missing country becomes nil, not
// the empty string.
type Result struct {
	Country  *string   `json:"country"`
	Region   *string   `json:"region"`
	Timezone *string   `json:"timezone"`
	City     *string   `json:"city"`
	LL       []float64 `json:"ll"`
	Metro    *int      `json:"metro"`
	Area     *int      `json:"area"`
}

// Provider returns a Result for an IP. nil means "not found" — the service
// then returns a zero-value Result with all-nil fields, matching TS.
type Provider func(ip string) *Result

// Service wraps a Provider behind the GeoIPService.lookup() API.
type Service struct {
	provider Provider
}

// New constructs a GeoIP Service backed by the given lookup provider.
// Pass nil for an always-not-found provider (useful in tests).
func New(provider Provider) *Service {
	if provider == nil {
		provider = NoopProvider
	}
	return &Service{provider: provider}
}

// Lookup returns geo info for ip, or an all-nil Result if not found.
func (s *Service) Lookup(ip string) Result {
	if r := s.provider(ip); r != nil {
		return *r
	}
	return Result{}
}

// NoopProvider always returns nil — the "not found" path. Useful as a default.
func NoopProvider(string) *Result { return nil }

// ── MapProvider ────────────────────────────────────────────────────────────────

// MapProvider returns a Provider backed by a static IP→Result map.
// Concurrent-safe; the underlying map is copied at construction.
func MapProvider(table map[string]Result) Provider {
	copy := make(map[string]Result, len(table))
	for k, v := range table {
		copy[k] = v
	}
	return func(ip string) *Result {
		r, ok := copy[ip]
		if !ok {
			return nil
		}
		return &r
	}
}

// ── HTTPProvider ───────────────────────────────────────────────────────────────

// HTTPProviderConfig configures a Provider that consults a remote geoip HTTP
// service. The default URL template targets ip-api.com (free for ≤45 req/min).
// Apps with higher volume should plug in their own (licensed) MMDB-backed
// Provider instead.
type HTTPProviderConfig struct {
	// URLTemplate is interpolated with `{ip}`. Default:
	// "http://ip-api.com/json/{ip}?fields=country,regionName,timezone,city,lat,lon".
	URLTemplate string
	// Timeout is the per-request timeout. Default: 3 seconds.
	Timeout time.Duration
	// Cache, when non-nil, memoises results for the configured TTL.
	Cache *MemoCache
}

// MemoCache is the in-memory TTL cache used by HTTPProvider. Construct via
// NewMemoCache(ttl) — the zero value is unusable.
type MemoCache struct {
	mu    sync.RWMutex
	store map[string]memoEntry
	ttl   time.Duration
}

type memoEntry struct {
	result    Result
	expiresAt time.Time
}

// NewMemoCache returns a MemoCache with the given TTL. ttl ≤ 0 disables
// expiry (entries are kept until the cache is dropped).
func NewMemoCache(ttl time.Duration) *MemoCache {
	return &MemoCache{store: map[string]memoEntry{}, ttl: ttl}
}

func (c *MemoCache) get(ip string) (Result, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.store[ip]
	if !ok {
		return Result{}, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		return Result{}, false
	}
	return e.result, true
}

func (c *MemoCache) set(ip string, r Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := memoEntry{result: r}
	if c.ttl > 0 {
		e.expiresAt = time.Now().Add(c.ttl)
	}
	c.store[ip] = e
}

// HTTPProvider constructs a Provider that delegates to a remote HTTP geoip
// service. Errors are swallowed and surface as a nil Result, matching the TS
// "not found" semantics for any failure mode.
func HTTPProvider(cfg HTTPProviderConfig) Provider {
	if cfg.URLTemplate == "" {
		cfg.URLTemplate = "http://ip-api.com/json/{ip}?fields=country,regionName,timezone,city,lat,lon"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	client := &http.Client{Timeout: cfg.Timeout}

	return func(ip string) *Result {
		if ip == "" {
			return nil
		}
		if cfg.Cache != nil {
			if r, ok := cfg.Cache.get(ip); ok {
				return &r
			}
		}

		req, err := http.NewRequest(http.MethodGet, strings.ReplaceAll(cfg.URLTemplate, "{ip}", url.PathEscape(ip)), nil)
		if err != nil {
			return nil
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil
		}

		// ip-api.com response: {country, regionName, timezone, city, lat, lon}.
		var raw struct {
			Country    string  `json:"country"`
			RegionName string  `json:"regionName"`
			Timezone   string  `json:"timezone"`
			City       string  `json:"city"`
			Lat        float64 `json:"lat"`
			Lon        float64 `json:"lon"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return nil
		}

		out := Result{
			LL: []float64{raw.Lat, raw.Lon},
		}
		if raw.Country != "" {
			c := raw.Country
			out.Country = &c
		}
		if raw.RegionName != "" {
			r := raw.RegionName
			out.Region = &r
		}
		if raw.Timezone != "" {
			t := raw.Timezone
			out.Timezone = &t
		}
		if raw.City != "" {
			c := raw.City
			out.City = &c
		}
		// Sentinel: a 0,0 LL pair from ip-api means "unknown" — return nil
		// instead so the caller can opt for a fallback. Matches geoip-lite,
		// which returns null for unknown IPs.
		if out.Country == nil && raw.Lat == 0 && raw.Lon == 0 {
			return nil
		}
		if cfg.Cache != nil {
			cfg.Cache.set(ip, out)
		}
		return &out
	}
}

// ── ChainProvider ──────────────────────────────────────────────────────────────

// ChainProvider returns a Provider that tries each member in turn, stopping at
// the first non-nil Result. Useful for wiring MapProvider (overrides) +
// HTTPProvider (catch-all): `geoip.New(geoip.ChainProvider(local, remote))`.
func ChainProvider(providers ...Provider) Provider {
	return func(ip string) *Result {
		for _, p := range providers {
			if p == nil {
				continue
			}
			if r := p(ip); r != nil {
				return r
			}
		}
		return nil
	}
}

// ErrProviderUnreachable is returned by callers that want to surface a hard
// HTTPProvider failure. The Provider itself swallows errors to match TS, so
// this is exposed as a typed signal for higher-level retry / circuit-break
// logic.
var ErrProviderUnreachable = errors.New("geoip: provider unreachable")

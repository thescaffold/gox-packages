// Package geoip ports ntx-packages/libs/core/src/services/geo-ip.service.ts.
//
// The TS service uses geoip-lite which embeds a MaxMind database. In Go you
// would typically wire MaxMind via github.com/oschwald/maxminddb-golang or use
// a public HTTP geoip provider. To keep core dependency-free, this package
// defines the Lookup contract and a Service that delegates to a Provider; tests
// inject a stub provider.
package geoip

// Result mirrors the TS GeoIPService.lookup() shape.
type Result struct {
	Country  *string   `json:"country"`
	Region   *string   `json:"region"`
	Timezone *string   `json:"timezone"`
	City     *string   `json:"city"`
	LL       []float64 `json:"ll"`
	Metro    *int      `json:"metro"`
	Area     *int      `json:"area"`
}

// Provider returns a Result for a given IP. nil means "not found" — the
// service then returns a zero-value Result with all-nil fields, matching TS.
type Provider func(ip string) *Result

// Service wraps a Provider behind the GeoIPService.lookup() API.
type Service struct {
	provider Provider
}

// New constructs a GeoIP Service backed by the given lookup provider.
// Pass nil for an always-not-found provider (useful in tests).
func New(provider Provider) *Service {
	return &Service{provider: provider}
}

// Lookup returns geo info for ip, or an all-nil Result if not found.
func (s *Service) Lookup(ip string) Result {
	if s.provider == nil {
		return Result{}
	}
	if r := s.provider(ip); r != nil {
		return *r
	}
	return Result{}
}

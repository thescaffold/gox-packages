package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages/libs/core/services/geoip"
)

func TestGeoIP(t *testing.T) {
	test.NewSuiteRunner(t, &GeoIPSuite{}).Run()
}

type GeoIPSuite struct {
	test.Suite
}

func strPtr(s string) *string { return &s }

// TestLookup_Noop returns zero-value Result (all nil fields) when the provider
// can't classify the IP — matches TS `lookup` returning all-null fields.
func (s *GeoIPSuite) TestLookup_Noop() {
	svc := geoip.New(nil)
	r := svc.Lookup("203.0.113.1")
	s.T.Expect(r.Country == nil).ToEqual(true)
	s.T.Expect(r.LL == nil).ToEqual(true)
}

// TestMapProvider returns the configured Result for known IPs.
func (s *GeoIPSuite) TestMapProvider() {
	table := map[string]geoip.Result{
		"8.8.8.8": {Country: strPtr("US"), City: strPtr("Mountain View")},
	}
	svc := geoip.New(geoip.MapProvider(table))
	r := svc.Lookup("8.8.8.8")
	s.T.Expect(r.Country != nil && *r.Country == "US").ToEqual(true)
	s.T.Expect(r.City != nil && *r.City == "Mountain View").ToEqual(true)
}

func (s *GeoIPSuite) TestMapProvider_Miss() {
	svc := geoip.New(geoip.MapProvider(map[string]geoip.Result{}))
	r := svc.Lookup("1.2.3.4")
	s.T.Expect(r.Country == nil).ToEqual(true)
}

// TestChainProvider tries each member in turn until one returns non-nil.
func (s *GeoIPSuite) TestChainProvider() {
	first := geoip.MapProvider(map[string]geoip.Result{"1.1.1.1": {Country: strPtr("AU")}})
	second := geoip.MapProvider(map[string]geoip.Result{"8.8.8.8": {Country: strPtr("US")}})
	svc := geoip.New(geoip.ChainProvider(first, second))
	s.T.Expect(*svc.Lookup("1.1.1.1").Country).ToEqual("AU")
	s.T.Expect(*svc.Lookup("8.8.8.8").Country).ToEqual("US")
	s.T.Expect(svc.Lookup("203.0.113.1").Country == nil).ToEqual(true)
}

// TestHTTPProvider stubs out the remote service via httptest and asserts the
// response gets mapped onto the Result fields.
func (s *GeoIPSuite) TestHTTPProvider() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"country":"United States",
			"regionName":"California",
			"timezone":"America/Los_Angeles",
			"city":"Mountain View",
			"lat":37.4192,
			"lon":-122.0574
		}`))
	}))
	defer ts.Close()

	provider := geoip.HTTPProvider(geoip.HTTPProviderConfig{
		URLTemplate: ts.URL + "/{ip}",
		Timeout:     500 * time.Millisecond,
	})
	svc := geoip.New(provider)
	r := svc.Lookup("8.8.8.8")
	s.T.Expect(r.Country != nil && *r.Country == "United States").ToEqual(true)
	s.T.Expect(r.City != nil && *r.City == "Mountain View").ToEqual(true)
	s.T.Expect(len(r.LL)).ToEqual(2)
	s.T.Expect(r.LL[0]).ToEqual(37.4192)
}

// TestHTTPProvider_Cache memoises results so repeat lookups don't hit the
// remote service.
func (s *GeoIPSuite) TestHTTPProvider_Cache() {
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"country":"US","lat":1.0,"lon":2.0}`))
	}))
	defer ts.Close()

	provider := geoip.HTTPProvider(geoip.HTTPProviderConfig{
		URLTemplate: ts.URL + "/{ip}",
		Cache:       geoip.NewMemoCache(time.Minute),
	})
	svc := geoip.New(provider)
	_ = svc.Lookup("1.2.3.4")
	_ = svc.Lookup("1.2.3.4")
	_ = svc.Lookup("1.2.3.4")
	s.T.Expect(hits).ToEqual(1)
}

// TestHTTPProvider_NetworkError_NotFound swallows transport errors and
// returns nil (matching TS lookup() which returns null on geoip-lite failure).
func (s *GeoIPSuite) TestHTTPProvider_NetworkError_NotFound() {
	provider := geoip.HTTPProvider(geoip.HTTPProviderConfig{
		URLTemplate: "http://127.0.0.1:1/unreachable/{ip}", // refused
		Timeout:     100 * time.Millisecond,
	})
	svc := geoip.New(provider)
	r := svc.Lookup("8.8.8.8")
	s.T.Expect(r.Country == nil).ToEqual(true)
}

// TestHTTPProvider_ZeroSentinel maps ip-api.com's "0,0,empty country" reply
// to a nil Result, mirroring geoip-lite's null-for-unknown behaviour.
func (s *GeoIPSuite) TestHTTPProvider_ZeroSentinel() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"country":"","lat":0,"lon":0}`))
	}))
	defer ts.Close()

	provider := geoip.HTTPProvider(geoip.HTTPProviderConfig{URLTemplate: ts.URL + "/{ip}"})
	svc := geoip.New(provider)
	r := svc.Lookup("0.0.0.0")
	s.T.Expect(r.Country == nil).ToEqual(true)
}

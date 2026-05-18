package tests

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	test "github.com/awesome-goose/goose/testing"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	corehttp "github.com/thescaffold/gox-packages/libs/core/http"
	"github.com/thescaffold/gox-packages/libs/core/services/platform"
)

func TestPlatform(t *testing.T) {
	test.NewSuiteRunner(t, &PlatformSuite{}).Run()
}

type PlatformSuite struct {
	test.Suite
}

// TestNew_BaseURLAccessible verifies the constructor stores and exposes the
// configured base URL with trailing slashes stripped.
func (s *PlatformSuite) TestNew_BaseURLAccessible() {
	svc := platform.New(corehttp.New("k"), "http://gateway/")
	s.T.Expect(svc.BaseURL()).ToEqual("http://gateway")
	s.T.Expect(svc.AppsBaseURL()).ToEqual("http://gateway/apps")
}

// TestNewWithBaseURLFn_DynamicResolution proves the BASE_URL is re-read on
// every call, mirroring TS configService.get('BASE_URL') semantics for env
// reloads at runtime.
func (s *PlatformSuite) TestNewWithBaseURLFn_DynamicResolution() {
	current := "http://a"
	svc := platform.NewWithBaseURLFn(corehttp.New("k"), func() string { return current })
	s.T.Expect(svc.BaseURL()).ToEqual("http://a")
	current = "http://b/"
	// New call re-reads the fn AND strips trailing slash.
	s.T.Expect(svc.BaseURL()).ToEqual("http://b")
}

// TestWithContext_ForwardsHeaders captures the outbound HMAC call to assert
// that the x-ntx-* headers derived from the NTXContext are forwarded — the
// real value of Wave 1.1 over the previous "headers passed as nil" wiring.
func (s *PlatformSuite) TestWithContext_ForwardsHeaders() {
	var captured http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data": {"ok": true}}`))
	}))
	defer ts.Close()

	svc := platform.New(corehttp.New("hmac-key"), ts.URL).
		WithContext(ntxctx.NTXContext{
			UserID:      "u-1",
			ClientID:    "c-1",
			WorkspaceID: "ws-1",
			Scope:       "self",
		})
	res := svc.CapitalGetStatus()
	s.T.Expect(res.Status).ToEqual(true)

	// Context-derived headers must appear on the outbound request.
	s.T.Expect(captured.Get("x-ntx-user-id")).ToEqual("u-1")
	s.T.Expect(captured.Get("x-ntx-client-id")).ToEqual("c-1")
	s.T.Expect(captured.Get("x-ntx-workspace-id")).ToEqual("ws-1")
	s.T.Expect(captured.Get("x-ntx-scope")).ToEqual("self")
	// HMAC signing still happens.
	s.T.Expect(strings.HasPrefix(captured.Get("authorization"), "hmac ")).ToEqual(true)
	// Tracing id is minted by WithContext when the source context has none.
	s.T.Expect(captured.Get("x-ntx-tracing-id") == "").ToEqual(false)
}

// TestWithContext_TracingIDPreserved confirms that an explicit tracing id from
// the inbound context is propagated verbatim (no over-mint).
func (s *PlatformSuite) TestWithContext_TracingIDPreserved() {
	var captured http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	svc := platform.New(corehttp.New("k"), ts.URL).
		WithContext(ntxctx.NTXContext{TracingID: "TID-PINNED"})
	_ = svc.CapitalGetStatus()
	s.T.Expect(captured.Get("x-ntx-tracing-id")).ToEqual("TID-PINNED")
}

// TestWithoutContext_NoLeakedHeaders ensures calls made on the bare service
// (no WithContext) do not carry x-ntx-user-id etc., matching the TS default.
func (s *PlatformSuite) TestWithoutContext_NoLeakedHeaders() {
	var captured http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	svc := platform.New(corehttp.New("k"), ts.URL)
	_ = svc.CapitalGetStatus()
	s.T.Expect(captured.Get("x-ntx-user-id")).ToEqual("")
	s.T.Expect(captured.Get("x-ntx-workspace-id")).ToEqual("")
}

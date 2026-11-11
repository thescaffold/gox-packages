package tests

import (
	"net/http/httptest"
	"testing"

	"github.com/awesome-goose/goose/platforms/api"
	"github.com/awesome-goose/goose/types"
	coreauth "github.com/thescaffold/gox-packages/libs/core/auth"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
)

// ipContext is a types.Context over a real api.Request.
type ipContext struct {
	req    types.Request
	values map[any]any
}

func (c *ipContext) Request() types.Request   { return c.req }
func (c *ipContext) Response() types.Response { return nil }
func (c *ipContext) SetValue(k, v any)        { c.values[k] = v }
func (c *ipContext) GetValue(k any) any       { return c.values[k] }

func newIPContext(peer string, headers map[string]string) *ipContext {
	r := httptest.NewRequest("POST", "/signup", nil)
	r.RemoteAddr = peer
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return &ipContext{req: api.NewRequest(r), values: map[any]any{}}
}

// The lane-2 finding: every IdentityLoginAttempts.ip was empty, so each per-address limit was one
// global bucket. The address now comes from the connection, through trusted proxies only.
func TestRequestInfoFillsTheClientAddress(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8")
	ctx := newIPContext("10.0.0.5:443", map[string]string{"X-Forwarded-For": "198.51.100.7"})
	if err := (&ntxctx.RequestInfo{}).Handle(ctx); err != nil {
		t.Fatal(err)
	}
	if got := ntxctx.Get(ctx); got.IP != "198.51.100.7" || got.TracingID == "" {
		t.Fatalf("RequestInfo: IP %q tracing %q", got.IP, got.TracingID)
	}

	spoof := newIPContext("203.0.113.9:5000", map[string]string{"X-Forwarded-For": "1.1.1.1", "X-Ntx-User-Id": "someone"})
	_ = (&ntxctx.RequestInfo{}).Handle(spoof)
	if got := ntxctx.Get(spoof); got.IP != "203.0.113.9" || got.UserID != "" {
		t.Fatalf("an untrusted peer chose its address or identity: %+v", got)
	}
}

func TestFromClaimsKeepsTheClientAddress(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "")
	ctx := newIPContext("203.0.113.9:5000", nil)
	coreauth.SetClaims(ctx, map[string]any{"sub": "u-1"})
	_ = (&coreauth.FromClaims{}).Handle(ctx)
	if got := ntxctx.Get(ctx); got.UserID != "u-1" || got.IP != "203.0.113.9" {
		t.Fatalf("FromClaims: %+v", got)
	}
}

func TestParseReadsCanonicalHeaderKeys(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("x-ntx-tracing-id", "tid-9") // net/http stores it as X-Ntx-Tracing-Id
	if got := ntxctx.Parse(r.Header); got.TracingID != "tid-9" {
		t.Fatalf("Parse missed a canonical header key: %q", got.TracingID)
	}
}

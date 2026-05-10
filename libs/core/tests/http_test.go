package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	test "github.com/awesome-goose/goose/testing"
	ntxhttp "github.com/thescaffold/gox-packages/libs/core/http"
)

func TestHttp(t *testing.T) {
	test.NewSuiteRunner(t, &HttpSuite{}).Run()
}

type HttpSuite struct {
	test.Suite
}

// mockServer starts a test HTTP server that records requests and returns a fixed response.
type mockServer struct {
	*httptest.Server
	lastMethod  string
	lastPath    string
	lastHeaders http.Header
	lastBody    []byte
	statusCode  int
	respBody    any
}

func newMockServer(statusCode int, respBody any) *mockServer {
	ms := &mockServer{statusCode: statusCode, respBody: respBody}
	ms.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ms.lastMethod = r.Method
		ms.lastPath = r.URL.Path
		ms.lastHeaders = r.Header.Clone()
		ms.lastBody, _ = json.Marshal(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(respBody)
	}))
	return ms
}

// ── External ───────────────────────────────────────────────────────────────────

func (s *HttpSuite) TestExternal_200_ReturnsSuccess() {
	srv := newMockServer(200, map[string]any{"ok": true})
	defer srv.Close()
	c := ntxhttp.New("secret")
	ok, status, _, _, data := c.External("GET", srv.URL+"/ping", nil, nil, nil, 0)
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(status).ToEqual(200)
	s.T.Expect(data == nil).ToEqual(false)
}

func (s *HttpSuite) TestExternal_404_ReturnsFalse() {
	srv := newMockServer(404, map[string]any{"error": "not found"})
	defer srv.Close()
	c := ntxhttp.New("secret")
	ok, status, _, _, _ := c.External("GET", srv.URL+"/missing", nil, nil, nil, 0)
	s.T.Expect(ok).ToEqual(false)
	s.T.Expect(status).ToEqual(404)
}

func (s *HttpSuite) TestExternal_POST_SendsBody() {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"created": true})
	}))
	defer srv.Close()
	c := ntxhttp.New("secret")
	ok, status, _, _, _ := c.External("POST", srv.URL+"/items", map[string]any{"name": "widget"}, nil, nil, 0)
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(status).ToEqual(201)
	s.T.Expect(received["name"]).ToEqual("widget")
}

func (s *HttpSuite) TestExternal_QueryParams_AppendedToURL() {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.Query().Get("page")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()
	c := ntxhttp.New("secret")
	c.External("GET", srv.URL+"/list", nil, map[string]string{"page": "3"}, nil, 0)
	s.T.Expect(receivedQuery).ToEqual("3")
}

// ── Internal ───────────────────────────────────────────────────────────────────

func (s *HttpSuite) TestInternal_AddsHMACHeaders() {
	var gotNonce, gotTimestamp, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotNonce = r.Header.Get("X-Ntx-Nonce")
		gotTimestamp = r.Header.Get("X-Ntx-Timestamp")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()
	c := ntxhttp.New("my-hmac-key")
	ok, _, _, _, _ := c.Internal("GET", srv.URL+"/secure", nil, nil, nil, 0)
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(gotNonce == "").ToEqual(false)
	s.T.Expect(gotTimestamp == "").ToEqual(false)
	s.T.Expect(gotAuth == "").ToEqual(false)
	// authorization header must start with "hmac "
	s.T.Expect(len(gotAuth) > 5).ToEqual(true)
}

func (s *HttpSuite) TestInternal_AuthorizationPrefixedHmac() {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(nil)
	}))
	defer srv.Close()
	c := ntxhttp.New("key")
	c.Internal("GET", srv.URL, nil, nil, nil, 0)
	s.T.Expect(gotAuth[:5]).ToEqual("hmac ")
}

// ── Retry logic ────────────────────────────────────────────────────────────────

func (s *HttpSuite) TestExternal_Retry_SucceedsOnSecondAttempt() {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()
	c := ntxhttp.New("secret")
	ok, status, _, _, _ := c.External("GET", srv.URL, nil, nil, nil, 1)
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(status).ToEqual(200)
	s.T.Expect(attempts).ToEqual(2)
}

func (s *HttpSuite) TestExternal_Retry_ExhaustsAllAttempts() {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c := ntxhttp.New("secret")
	ok, _, _, _, _ := c.External("GET", srv.URL, nil, nil, nil, 2)
	s.T.Expect(ok).ToEqual(false)
	s.T.Expect(attempts).ToEqual(3) // 1 initial + 2 retries
}

// ── Unreachable host ───────────────────────────────────────────────────────────

func (s *HttpSuite) TestExternal_UnreachableHost_Returns503() {
	c := ntxhttp.New("secret")
	c.HTTPClient.Timeout = 0 // let it fail fast with invalid host
	ok, status, _, _, _ := c.External("GET", "http://127.0.0.1:1", nil, nil, nil, 0)
	s.T.Expect(ok).ToEqual(false)
	s.T.Expect(status).ToEqual(503)
}

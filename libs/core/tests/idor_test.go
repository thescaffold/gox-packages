package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages/libs/core/idor"
)

func TestIDOR(t *testing.T) {
	test.NewSuiteRunner(t, &IDORSuite{}).Run()
}

type IDORSuite struct {
	test.Suite
}

// fakeCrudServer is a tiny stand-in for a real crud.CrudResource-backed
// route: enough to prove idor.Check's own detection logic against a known
// "safe" (correctly workspace-scoped) and "leaky" (not scoped, or only
// scoped on Create — exactly PLAN M1-02a's pre-fix CrudResource) server,
// without needing a real goose boot or Postgres. Bearer token *is* the
// workspace id here — this harness only cares about the envelope shape, not
// how a real deployment derives ctx.WorkspaceID from a JWT.
type fakeCrudServer struct {
	mu      sync.Mutex
	items   map[string]map[string]any
	nextID  int
	scoped  bool // true = List/Get/Update/Delete honor the bearer token
}

func newFakeCrudServer(scoped bool) *fakeCrudServer {
	return &fakeCrudServer{items: map[string]map[string]any{}, scoped: scoped}
}

func (s *fakeCrudServer) token(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func writeEnvelope(w http.ResponseWriter, code int, status string, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "data": data})
}

func (s *fakeCrudServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/items")
	id := strings.TrimPrefix(path, "/")
	token := s.token(r)

	switch {
	case r.Method == http.MethodPost && id == "":
		s.nextID++
		newID := strconv.Itoa(s.nextID)
		body, _ := io.ReadAll(r.Body)
		var fields map[string]any
		_ = json.Unmarshal(body, &fields)
		if fields == nil {
			fields = map[string]any{}
		}
		fields["id"] = newID
		fields["workspace"] = token // mirrors Create's own BeforeCreate-hook convention — always attributed, on every fixture
		s.items[newID] = fields
		writeEnvelope(w, http.StatusOK, "success", fields)

	case r.Method == http.MethodGet && id == "":
		var rows []map[string]any
		for _, row := range s.items {
			if !s.scoped || row["workspace"] == token {
				rows = append(rows, row)
			}
		}
		writeEnvelope(w, http.StatusOK, "success", rows)

	case r.Method == http.MethodGet && id != "":
		row, ok := s.items[id]
		if !ok || (s.scoped && row["workspace"] != token) {
			writeEnvelope(w, http.StatusNotFound, "error", nil)
			return
		}
		writeEnvelope(w, http.StatusOK, "success", row)

	case r.Method == http.MethodPatch && id != "":
		row, ok := s.items[id]
		if !ok || (s.scoped && row["workspace"] != token) {
			writeEnvelope(w, http.StatusBadRequest, "error", nil)
			return
		}
		writeEnvelope(w, http.StatusOK, "success", map[string]any{"id": id})

	case r.Method == http.MethodDelete && id != "":
		row, ok := s.items[id]
		if !ok || (s.scoped && row["workspace"] != token) {
			writeEnvelope(w, http.StatusNotFound, "error", nil)
			return
		}
		delete(s.items, id)
		writeEnvelope(w, http.StatusOK, "success", nil)

	default:
		writeEnvelope(w, http.StatusMethodNotAllowed, "error", nil)
	}
}

func createFixture(name, basePath string) idor.Fixture {
	return idor.Fixture{
		Name:     name,
		BasePath: basePath,
		Create: func(client *http.Client, basePath, token string) (string, error) {
			req, _ := http.NewRequest(http.MethodPost, basePath, strings.NewReader(`{"name":"Acme"}`))
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := client.Do(req)
			if err != nil {
				return "", err
			}
			defer func() { _ = resp.Body.Close() }()
			var env struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				return "", err
			}
			return env.Data.ID, nil
		},
	}
}

// This is PLAN M1-09's own Done criterion made literal: the harness must
// fail on a deliberately leaky fixture endpoint, not just pass on a correct
// one. scoped=false reproduces exactly the pre-M1-02a CrudResource shape —
// Create attributes a workspace, but List/Get/Update/Delete never check it.
func (s *IDORSuite) TestCheck_FlagsEveryVerbOnATrulyUnscopedResource() {
	srv := httptest.NewServer(newFakeCrudServer(false))
	defer srv.Close()

	violations, err := idor.Check(srv.Client(), "ws-a", "ws-b", []idor.Fixture{
		createFixture("leaky", srv.URL+"/items"),
	})
	s.T.Expect(err).ToBeNil()

	got := map[string]bool{}
	for _, v := range violations {
		got[v.Check] = true
	}
	s.T.Expect(got["list"]).ToEqual(true)
	s.T.Expect(got["get"]).ToEqual(true)
	s.T.Expect(got["update"]).ToEqual(true)
	s.T.Expect(got["delete"]).ToEqual(true)
	// A leaky resource doesn't also fail the owner-regression check — the
	// owner was never the problem.
	s.T.Expect(got["owner-regression"]).ToEqual(false)
}

// The mirror image: a correctly workspace-scoped resource (matching what
// M1-02a's WorkspaceScoped flag now produces) must report zero violations —
// otherwise the harness would be crying wolf on every real, already-fixed
// resource in cloud/server.
func (s *IDORSuite) TestCheck_NoViolationsOnACorrectlyScopedResource() {
	srv := httptest.NewServer(newFakeCrudServer(true))
	defer srv.Close()

	violations, err := idor.Check(srv.Client(), "ws-a", "ws-b", []idor.Fixture{
		createFixture("safe", srv.URL+"/items"),
	})
	s.T.Expect(err).ToBeNil()
	s.T.Expect(len(violations)).ToEqual(0)
}

// A scoping bug that fails closed *too* aggressively — rejecting the owner's
// own token, not just the attacker's — must be caught too: silently
// swallowing that case would let someone "fix" a leak by breaking access for
// everyone, and a harness only checking the attacker side wouldn't notice.
func (s *IDORSuite) TestCheck_FlagsOwnerRegression() {
	srv := httptest.NewServer(newFakeCrudServer(true))
	defer srv.Close()
	fixture := createFixture("over-scoped", srv.URL+"/items")

	// A resource that scopes correctly for List/Update/Delete but has a bug
	// in Get that rejects *everyone*, owner included.
	brokenGet := idor.Fixture{
		Name:     fixture.Name,
		BasePath: fixture.BasePath,
		Create:   fixture.Create,
	}
	// Reuse the same server but wrap it so GET /items/:id always 404s,
	// simulating that specific bug in isolation.
	mux := http.NewServeMux()
	mux.Handle("/items", srv.Config.Handler)
	mux.HandleFunc("/items/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeEnvelope(w, http.StatusNotFound, "error", nil)
			return
		}
		srv.Config.Handler.ServeHTTP(w, r)
	})
	broken := httptest.NewServer(mux)
	defer broken.Close()
	brokenGet.BasePath = broken.URL + "/items"

	violations, err := idor.Check(broken.Client(), "ws-a", "ws-b", []idor.Fixture{brokenGet})
	s.T.Expect(err).ToBeNil()

	found := false
	for _, v := range violations {
		if v.Check == "owner-regression" {
			found = true
		}
		// The attacker-side checks must still be clean — this resource
		// isn't leaky, just over-broken on Get.
		s.T.Expect(v.Check == "list" || v.Check == "update" || v.Check == "delete").ToEqual(false)
	}
	s.T.Expect(found).ToEqual(true)
}

func (s *IDORSuite) TestRunSuite_ReportsViolationsAsTestFailures() {
	srv := httptest.NewServer(newFakeCrudServer(false))
	defer srv.Close()

	// RunSuite calls t.Errorf per violation — verified via a throwaway
	// *testing.T whose own Fail state we inspect, so this test itself stays
	// green while still proving RunSuite surfaces failures correctly.
	fake := &testing.T{}
	idor.RunSuite(fake, srv.Client(), "ws-a", "ws-b", []idor.Fixture{
		createFixture("leaky", srv.URL+"/items"),
	})
	s.T.Expect(fake.Failed()).ToEqual(true)
}

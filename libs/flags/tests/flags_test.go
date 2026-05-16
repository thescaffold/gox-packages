package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	test "github.com/awesome-goose/goose/testing"
	corehttp "github.com/thescaffold/gox-packages/libs/core/http"
	"github.com/thescaffold/gox-packages/libs/flags"
	"github.com/thescaffold/gox-packages/libs/flags/flag"
)

func TestFlags(t *testing.T) {
	test.NewSuiteRunner(t, &FlagsSuite{}).Run()
}

type FlagsSuite struct {
	test.Suite
}

// scaffoldRecorder stubs the scaffold flags server.
type scaffoldRecorder struct {
	*httptest.Server
	authHeader string
	lastPath   string
	lastBody   map[string]any
	respData   any
}

func newScaffoldRecorder(respData any) *scaffoldRecorder {
	r := &scaffoldRecorder{respData: respData}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.authHeader = req.Header.Get("Authorization")
		r.lastPath = req.URL.Path
		_ = json.NewDecoder(req.Body).Decode(&r.lastBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   r.respData,
		})
	}))
	return r
}

func newSvc(server, credential string) *flag.FlagService {
	return flag.NewFlagService(flag.Config{
		Server: server, Credential: credential, SourceId: "src-1",
	}, corehttp.New(""))
}

// ── Register ──────────────────────────────────────────────────────────────────

func (s *FlagsSuite) TestRegister_PostsExpectedBody() {
	srv := newScaffoldRecorder(true)
	defer srv.Close()
	svc := newSvc(srv.URL, "tok")

	ok, err := svc.Register([]flag.Flag{
		{Name: "dark-mode", Limit: 100, Priority: 1, Level: "info"},
	}, "")
	s.T.Expect(err).ToBeNil()
	s.T.Expect(ok).ToEqual(true)

	s.T.Expect(srv.lastPath).ToEqual("/apps/flags/register")
	s.T.Expect(srv.authHeader).ToEqual("bearer tok")

	// jsx-flags register() defaults environmentTypeName to "Javascript".
	envType, _ := srv.lastBody["environmentType"].(map[string]any)
	s.T.Expect(envType["name"]).ToEqual("Javascript")
	env, _ := srv.lastBody["environment"].(map[string]any)
	s.T.Expect(env["name"]).ToEqual("src-1")
}

func (s *FlagsSuite) TestRegister_CustomEnvironmentTypeName() {
	srv := newScaffoldRecorder(true)
	defer srv.Close()
	svc := newSvc(srv.URL, "tok")

	_, _ = svc.Register([]flag.Flag{{Name: "f", Limit: 1, Priority: 1, Level: "info"}}, "Lambda")
	envType, _ := srv.lastBody["environmentType"].(map[string]any)
	s.T.Expect(envType["name"]).ToEqual("Lambda")
}

// ── Log ───────────────────────────────────────────────────────────────────────

func (s *FlagsSuite) TestLog_PostsToLogEndpoint() {
	srv := newScaffoldRecorder(true)
	defer srv.Close()
	svc := newSvc(srv.URL, "tok")

	level, userID := "info", "u-1"
	_, err := svc.Log("checkout", flag.LogOpts{Level: &level, UserID: &userID})
	s.T.Expect(err).ToBeNil()
	s.T.Expect(srv.lastPath).ToEqual("/apps/flags/log")
	s.T.Expect(srv.lastBody["name"]).ToEqual("checkout")
	s.T.Expect(srv.lastBody["level"]).ToEqual("info")
	s.T.Expect(srv.lastBody["userId"]).ToEqual("u-1")
	// limit and other unsupplied keys must be absent
	_, hasLimit := srv.lastBody["limit"]
	s.T.Expect(hasLimit).ToEqual(false)
}

// ── Status (name normalization) ──────────────────────────────────────────────

func (s *FlagsSuite) TestStatus_StringName_NormalizesTo2D() {
	srv := newScaffoldRecorder(map[string]any{"checkout": true})
	defer srv.Close()
	svc := newSvc(srv.URL, "tok")
	_, err := svc.Status("checkout", flag.StatusOpts{})
	s.T.Expect(err).ToBeNil()

	names, ok := srv.lastBody["names"].([]any)
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(len(names)).ToEqual(1)
	row, _ := names[0].([]any)
	s.T.Expect(row[0]).ToEqual("checkout")
}

func (s *FlagsSuite) TestStatus_1DSlice_NormalizesTo2D() {
	srv := newScaffoldRecorder(map[string]any{"a": true, "b": false})
	defer srv.Close()
	svc := newSvc(srv.URL, "tok")
	_, err := svc.Status([]string{"a", "b"}, flag.StatusOpts{})
	s.T.Expect(err).ToBeNil()
	names, _ := srv.lastBody["names"].([]any)
	s.T.Expect(len(names)).ToEqual(1)
	row, _ := names[0].([]any)
	s.T.Expect(len(row)).ToEqual(2)
}

func (s *FlagsSuite) TestStatus_2DSlice_PassesThrough() {
	srv := newScaffoldRecorder(map[string]any{})
	defer srv.Close()
	svc := newSvc(srv.URL, "tok")
	_, err := svc.Status([][]string{{"a"}, {"b", "c"}}, flag.StatusOpts{})
	s.T.Expect(err).ToBeNil()
	names, _ := srv.lastBody["names"].([]any)
	s.T.Expect(len(names)).ToEqual(2)
}

// ── Limit ─────────────────────────────────────────────────────────────────────

func (s *FlagsSuite) TestLimit_DecodesResponse() {
	srv := newScaffoldRecorder(map[string]any{
		"allowed": true, "limit": float64(100), "usage": float64(42),
	})
	defer srv.Close()
	svc := newSvc(srv.URL, "tok")

	res, err := svc.Limit("checkout", flag.LimitOpts{})
	s.T.Expect(err).ToBeNil()
	s.T.Expect(res.Allowed).ToEqual(true)
	s.T.Expect(res.Limit).ToEqual(100)
	s.T.Expect(res.Usage).ToEqual(42)
	s.T.Expect(srv.lastPath).ToEqual("/apps/flags/limit")
}

// ── Module registration ──────────────────────────────────────────────────────

func (s *FlagsSuite) TestRegisterModule_ValidConfig_ReturnsModule() {
	m := flags.Register(flags.FlagsConfig{
		Server: "http://example.test", Credential: "tok", SourceId: "src",
	})
	s.T.Expect(m == nil).ToEqual(false)
}

func (s *FlagsSuite) TestRegisterModule_MissingServer_Panics() {
	defer func() { s.T.Expect(recover() == nil).ToEqual(false) }()
	flags.Register(flags.FlagsConfig{Credential: "t", SourceId: "src"})
}

func (s *FlagsSuite) TestRegisterModule_MissingSourceId_Panics() {
	defer func() { s.T.Expect(recover() == nil).ToEqual(false) }()
	flags.Register(flags.FlagsConfig{Server: "http://x", Credential: "t"})
}

func (s *FlagsSuite) TestDeclarations_HasOneFlagService() {
	m := flags.Register(flags.FlagsConfig{
		Server: "http://example.test", Credential: "tok", SourceId: "src",
	})
	s.T.Expect(len(m.Declarations())).ToEqual(1)
}

func (s *FlagsSuite) TestExports_MatchDeclarations() {
	m := flags.Register(flags.FlagsConfig{
		Server: "http://example.test", Credential: "tok", SourceId: "src",
	})
	s.T.Expect(len(m.Exports())).ToEqual(len(m.Declarations()))
}

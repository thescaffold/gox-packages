package tests

import (
	"testing"
	"time"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages/libs/core/auth"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/services"
	"github.com/thescaffold/gox-packages/libs/core/services/throttler"
)

func TestAuth(t *testing.T) {
	test.NewSuiteRunner(t, &AuthSuite{}).Run()
}

type AuthSuite struct {
	test.Suite
}

const testSecret = "super-secret-key"

// ── JWT Sign / Verify ──────────────────────────────────────────────────────────

func (s *AuthSuite) TestSign_ProducesNonEmptyToken() {
	token, err := auth.Sign(map[string]any{"userId": "u1"}, testSecret, time.Hour)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(token == "").ToEqual(false)
}

func (s *AuthSuite) TestVerify_ValidToken_ReturnsClaims() {
	token, _ := auth.Sign(map[string]any{"userId": "u1", "role": "admin"}, testSecret, time.Hour)
	claims, err := auth.Verify(token, testSecret)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(claims["userId"]).ToEqual("u1")
	s.T.Expect(claims["role"]).ToEqual("admin")
}

func (s *AuthSuite) TestVerify_WrongSecret_ReturnsError() {
	token, _ := auth.Sign(map[string]any{"userId": "u1"}, testSecret, time.Hour)
	_, err := auth.Verify(token, "wrong-secret")
	s.T.Expect(err == nil).ToEqual(false)
}

func (s *AuthSuite) TestVerify_ExpiredToken_ReturnsError() {
	token, _ := auth.Sign(map[string]any{"userId": "u1"}, testSecret, -time.Second)
	_, err := auth.Verify(token, testSecret)
	s.T.Expect(err == nil).ToEqual(false)
}

func (s *AuthSuite) TestVerify_MalformedToken_ReturnsError() {
	_, err := auth.Verify("not.a.jwt", testSecret)
	s.T.Expect(err == nil).ToEqual(false)
}

func (s *AuthSuite) TestSign_NoExpiry_TokenHasNoExp() {
	token, err := auth.Sign(map[string]any{"sub": "x"}, testSecret, 0)
	s.T.Expect(err).ToBeNil()
	claims, err := auth.Verify(token, testSecret)
	s.T.Expect(err).ToBeNil()
	_, hasExp := claims["exp"]
	s.T.Expect(hasExp).ToEqual(false)
}

// ── Scope ──────────────────────────────────────────────────────────────────────

func (s *AuthSuite) TestGetScopeValue_PlainString_ReturnedUnchanged() {
	ctx := map[string]any{"userId": "u42"}
	result := auth.GetScopeValue("hello", ctx)
	s.T.Expect(result).ToEqual("hello")
}

func (s *AuthSuite) TestGetScopeValue_DottedPath_ResolvesFromContext() {
	ctx := map[string]any{"user": map[string]any{"id": "u42"}}
	result := auth.GetScopeValue("user.id", ctx)
	s.T.Expect(result).ToEqual("u42")
}

func (s *AuthSuite) TestGetScopeValue_MissingPath_ReturnOriginal() {
	ctx := map[string]any{"userId": "u42"}
	result := auth.GetScopeValue("user.email", ctx)
	s.T.Expect(result).ToEqual("user.email")
}

func (s *AuthSuite) TestGetScopeValue_NonString_ReturnedUnchanged() {
	ctx := map[string]any{}
	result := auth.GetScopeValue(42, ctx)
	s.T.Expect(result).ToEqual(42)
}

func (s *AuthSuite) TestExpandScopeObject_FlatKey() {
	ntx := ntxctx.NTXContext{UserID: "u99"}
	obj := map[string]any{"userId": "userId"}
	result := auth.ExpandScopeObject(obj, ntx)
	s.T.Expect(result["userId"]).ToEqual("u99")
}

func (s *AuthSuite) TestExpandScopeObject_DottedKey_CreatesNestedMap() {
	ntx := ntxctx.NTXContext{WorkspaceID: "ws1"}
	obj := map[string]any{"workspace.id": "workspaceId"}
	result := auth.ExpandScopeObject(obj, ntx)
	inner, ok := result["workspace"].(map[string]any)
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(inner["id"]).ToEqual("ws1")
}

func (s *AuthSuite) TestExpandScopeObject_LiteralValue_KeptAsIs() {
	ntx := ntxctx.NTXContext{}
	obj := map[string]any{"status": "active"}
	result := auth.ExpandScopeObject(obj, ntx)
	s.T.Expect(result["status"]).ToEqual("active")
}

// ── AuthMiddleware ─────────────────────────────────────────────────────────────

func (s *AuthSuite) TestAuthMiddleware_ValidToken_StoresClaims() {
	token, _ := auth.Sign(map[string]any{"userId": "u1"}, testSecret, time.Hour)
	mw := &auth.AuthMiddleware{Secret: testSecret}
	ctx := test.NewMockContext()
	ctx.MockRequest().WithHeader("authorization", "Bearer "+token)
	err := mw.Handle(ctx)
	s.T.Expect(err).ToBeNil()
	claims := auth.GetClaims(ctx)
	s.T.Expect(claims == nil).ToEqual(false)
	s.T.Expect(claims["userId"]).ToEqual("u1")
}

// TestAuthMiddleware_CanonicalHeaderCasing_StoresClaims reproduces a real
// production bug found while wiring identity/app/auth's requireUser()
// middleware for PLAN M0-27a: Handle looks up headers["authorization"]
// (all-lowercase) directly in the map, but goose's real HTTP-backed
// platforms (platforms/api/request.go's Headers(), which returns Go's
// stdlib http.Header) always store header keys in their canonical form —
// "Authorization" for this one, per textproto.CanonicalMIMEHeaderKey — so a
// real `Authorization: Bearer <token>` request header, however a client
// sends it, is stored under the capitalized key and the lowercase map
// lookup misses it every time. Every existing test above only exercises
// this via MockRequest.WithHeader("authorization", ...), which stores keys
// verbatim with no canonicalization — that's why they all pass despite the
// live bug; they never present headers the way a real HTTP server does.
func (s *AuthSuite) TestAuthMiddleware_CanonicalHeaderCasing_StoresClaims() {
	token, _ := auth.Sign(map[string]any{"userId": "u1"}, testSecret, time.Hour)
	mw := &auth.AuthMiddleware{Secret: testSecret}
	ctx := test.NewMockContext()
	ctx.MockRequest().WithHeader("Authorization", "Bearer "+token)
	err := mw.Handle(ctx)
	s.T.Expect(err).ToBeNil()
	claims := auth.GetClaims(ctx)
	s.T.Expect(claims == nil).ToEqual(false)
	s.T.Expect(claims["userId"]).ToEqual("u1")
}

func (s *AuthSuite) TestAuthMiddleware_MissingHeader_Returns401() {
	mw := &auth.AuthMiddleware{Secret: testSecret}
	ctx := test.NewMockContext()
	err := mw.Handle(ctx)
	s.T.Expect(err == nil).ToEqual(false)
	s.T.Expect(ctx.MockResponse().StatusCode()).ToEqual(401)
}

func (s *AuthSuite) TestAuthMiddleware_InvalidToken_Returns401() {
	mw := &auth.AuthMiddleware{Secret: testSecret}
	ctx := test.NewMockContext()
	ctx.MockRequest().WithHeader("authorization", "Bearer bad.token.here")
	err := mw.Handle(ctx)
	s.T.Expect(err == nil).ToEqual(false)
	s.T.Expect(ctx.MockResponse().StatusCode()).ToEqual(401)
}

func (s *AuthSuite) TestAuthMiddleware_ExpiredToken_Returns401() {
	token, _ := auth.Sign(map[string]any{"userId": "u1"}, testSecret, -time.Second)
	mw := &auth.AuthMiddleware{Secret: testSecret}
	ctx := test.NewMockContext()
	ctx.MockRequest().WithHeader("authorization", "Bearer "+token)
	err := mw.Handle(ctx)
	s.T.Expect(err == nil).ToEqual(false)
	s.T.Expect(ctx.MockResponse().StatusCode()).ToEqual(401)
}

// ── FromClaims ─────────────────────────────────────────────────────────────────

// TestFromClaims_PopulatesFullContextFromVerifiedClaims is PLAN (Origine)
// M1-02's own literal repro turned into a test: a token carrying
// workspaceId/roles/permissions must produce an NTXContext whose Workspace
// map matches — the exact shape ctx.Workspace["id"] lookups throughout
// crud.CrudResource's BeforeCreate hooks already expect.
func (s *AuthSuite) TestFromClaims_PopulatesFullContextFromVerifiedClaims() {
	token, _ := auth.Sign(map[string]any{
		"sub": "u1", "clientId": "c1", "workspaceId": "ws1",
		"roles": []string{"owner"}, "permissions": []string{"apps:systems:*"},
	}, testSecret, time.Hour)

	ctx := test.NewMockContext()
	ctx.MockRequest().WithHeader("Authorization", "Bearer "+token)

	authMw := &auth.AuthMiddleware{Secret: testSecret}
	s.T.Expect(authMw.Handle(ctx)).ToBeNil()

	fromClaims := &auth.FromClaims{}
	s.T.Expect(fromClaims.Handle(ctx)).ToBeNil()

	ntx := ntxctx.Get(ctx)
	s.T.Expect(ntx.UserID).ToEqual("u1")
	s.T.Expect(ntx.ClientID).ToEqual("c1")
	s.T.Expect(ntx.WorkspaceID).ToEqual("ws1")
	id, ok := ntx.Workspace["id"].(string)
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(id).ToEqual("ws1")
	s.T.Expect(len(ntx.Roles)).ToEqual(1)
	s.T.Expect(len(ntx.Permissions)).ToEqual(1)
}

// TestFromClaims_NoAuthMiddlewareRun_ProducesEmptyContext is the specific
// regression this exists to prevent understood in reverse: with no verified
// claims at all (AuthMiddleware never ran, or the request had none),
// FromClaims must set an empty context — never fall back to reading
// anything from the raw request the way ntxctx.Middleware does.
func (s *AuthSuite) TestFromClaims_NoAuthMiddlewareRun_ProducesEmptyContext() {
	ctx := test.NewMockContext()
	ctx.MockRequest().WithHeader("x-ntx-workspace-id", "attacker-chosen-workspace")

	fromClaims := &auth.FromClaims{}
	s.T.Expect(fromClaims.Handle(ctx)).ToBeNil()

	ntx := ntxctx.Get(ctx)
	s.T.Expect(ntx.WorkspaceID).ToEqual("")
	s.T.Expect(ntx.Workspace == nil).ToEqual(true)
}

// TestFromClaims_ClaimsWithNoWorkspace_LeavesWorkspaceEmpty checks a token
// that never had a workspace selected (an edge case identity's Login must
// stop producing once M1-02's other half lands, but FromClaims itself must
// be defensive regardless): no workspaceId claim means no Workspace map, not
// an empty-string id that would round-trip as "true" through a bare
// ctx.Workspace != nil check elsewhere.
func (s *AuthSuite) TestFromClaims_ClaimsWithNoWorkspace_LeavesWorkspaceEmpty() {
	token, _ := auth.Sign(map[string]any{"sub": "u1", "clientId": "c1"}, testSecret, time.Hour)
	ctx := test.NewMockContext()
	ctx.MockRequest().WithHeader("Authorization", "Bearer "+token)

	authMw := &auth.AuthMiddleware{Secret: testSecret}
	s.T.Expect(authMw.Handle(ctx)).ToBeNil()
	fromClaims := &auth.FromClaims{}
	s.T.Expect(fromClaims.Handle(ctx)).ToBeNil()

	ntx := ntxctx.Get(ctx)
	s.T.Expect(ntx.Workspace == nil).ToEqual(true)
}

// ── StatusGuard ────────────────────────────────────────────────────────────────

// TestStatusGuard_NilPlatform_AllowsThrough mirrors TS:
// when the platform call fails, "we return false & allow user to try again" —
// gox interprets that as "do not block".
func (s *AuthSuite) TestStatusGuard_NilPlatform_AllowsThrough() {
	g := &auth.StatusGuard{}
	ctx := test.NewMockContext()
	err := g.Handle(ctx)
	s.T.Expect(err).ToBeNil()
}

// ── UserThrottleGuard ──────────────────────────────────────────────────────────

func (s *AuthSuite) TestUserThrottleGuard_NilThrottler_AllowsThrough() {
	g := &auth.UserThrottleGuard{}
	ctx := test.NewMockContext()
	err := g.Handle(ctx)
	s.T.Expect(err).ToBeNil()
}

func (s *AuthSuite) TestUserThrottleGuard_NoIDFound_AllowsThrough() {
	cache := services.NewMemoryBackend()
	tr := throttler.New(cache, time.Minute, 60)
	g := &auth.UserThrottleGuard{Throttler: tr}
	ctx := test.NewMockContext() // no claims, no body
	err := g.Handle(ctx)
	s.T.Expect(err).ToBeNil()
}

// TestUserThrottleGuard_BlocksOverLimit confirms the guard returns 429 once
// the throttler reports `allowed=false`. Mirrors TS canActivate returning a
// HttpException(TOO_MANY_REQUESTS).
func (s *AuthSuite) TestUserThrottleGuard_BlocksOverLimit() {
	cache := services.NewMemoryBackend()
	tr := throttler.New(cache, time.Minute, 1) // limit = 1
	g := &auth.UserThrottleGuard{Throttler: tr}

	// First hit registers the bucket — allow.
	ctx1 := test.NewMockContext()
	// Inject a fake claim via the auth middleware token path.
	tok, _ := auth.Sign(map[string]any{"id": "u-throttled"}, testSecret, time.Hour)
	ctx1.MockRequest().WithHeader("authorization", "Bearer "+tok)
	mw := &auth.AuthMiddleware{Secret: testSecret}
	_ = mw.Handle(ctx1)
	err := g.Handle(ctx1)
	s.T.Expect(err).ToBeNil()

	// Second hit exceeds limit — block.
	ctx2 := test.NewMockContext()
	ctx2.MockRequest().WithHeader("authorization", "Bearer "+tok)
	_ = mw.Handle(ctx2)
	err = g.Handle(ctx2)
	s.T.Expect(err == nil).ToEqual(false)
	s.T.Expect(ctx2.MockResponse().StatusCode()).ToEqual(429)
}

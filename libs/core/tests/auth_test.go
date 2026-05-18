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

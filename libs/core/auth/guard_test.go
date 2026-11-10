package auth

import (
	"fmt"
	"testing"
	"time"

	"github.com/awesome-goose/goose/modules/router"
	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/security"
)

func hasGuard[T any](mws []types.Middleware) bool {
	for _, m := range mws {
		if _, ok := m.(T); ok {
			return true
		}
	}
	return false
}

func handlerMiddlewares(r types.Route) map[string][]types.Middleware {
	out := map[string][]types.Middleware{}
	for _, c := range r.Children {
		out[c.Handler.([]any)[1].(string)] = c.Middlewares
	}
	return out
}

func TestPlatformAdmin(t *testing.T) {
	t.Setenv("ROOT_USER_ID", "root-1")
	cases := []struct {
		ntx  ntxctx.NTXContext
		want bool
	}{
		{ntxctx.NTXContext{UserID: "root-1"}, true},
		{ntxctx.NTXContext{UserID: "u", Roles: []any{"Global_Admin"}}, true},
		{ntxctx.NTXContext{UserID: "u", Roles: []any{"root"}}, true},
		// every self-serve user owns a workspace: that is not platform power
		{ntxctx.NTXContext{UserID: "u", WorkspaceID: "w", Roles: []any{"owner", "admin"}}, false},
		{ntxctx.NTXContext{Roles: []any{"root"}}, false},
	}
	for i, c := range cases {
		if got := PlatformAdmin(c.ntx); got != c.want {
			t.Errorf("case %d: PlatformAdmin = %v, want %v", i, got, c.want)
		}
	}
}

func TestSecureAdminAndCallerChains(t *testing.T) {
	admin := handlerMiddlewares(SecureAdmin(router.Resource("thing", dummy{}).All(), "List"))
	if len(admin["List"]) != 0 {
		t.Error("a public handler was guarded")
	}
	for _, h := range []string{"Get", "Create", "Update", "Delete"} {
		if !hasGuard[*AuthMiddleware](admin[h]) || !hasGuard[*FromClaims](admin[h]) || !hasGuard[*AdminGuard](admin[h]) {
			t.Errorf("SecureAdmin %s lacks session, context or admin guard", h)
		}
	}
	caller := handlerMiddlewares(SecureCaller(router.Resource("thing", dummy{}).All()))
	for h, mws := range caller {
		if !hasGuard[*AuthMiddleware](mws) || !hasGuard[*FromClaims](mws) || hasGuard[*AdminGuard](mws) {
			t.Errorf("SecureCaller %s: want session and context, no admin guard", h)
		}
	}
}

func TestAdminWritesSplitsReadsFromWrites(t *testing.T) {
	got := handlerMiddlewares(AdminWrites(router.Resource("thing", dummy{}).All()))
	for _, h := range []string{"List", "Get"} {
		if !hasGuard[*FromClaims](got[h]) || hasGuard[*AdminGuard](got[h]) {
			t.Errorf("%s should be open to a verified caller", h)
		}
	}
	for _, h := range []string{"Create", "Update", "Delete"} {
		if !hasGuard[*AdminGuard](got[h]) {
			t.Errorf("%s should need a platform admin", h)
		}
	}
	if !RequiresSession(got["List"]) || !RequiresSession(got["Create"]) || !RequiresSession(Internal()) {
		t.Error("guarded chains must be documented as secured")
	}
}

func sign(t *testing.T, key, nonce, ts string) string {
	t.Helper()
	sig, err := security.GenerateHmac(map[string]any{"nonce": nonce, "timestamp": ts}, "sha256", key)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func TestVerifyInternal(t *testing.T) {
	t.Setenv("HMAC_KEY", "k-1")
	now := time.Now()
	ts := fmt.Sprintf("%d", now.UnixMilli())
	if !VerifyInternal(sign(t, "k-1", "n-1", ts), "n-1", ts, now) {
		t.Fatal("a fresh, correctly signed call was refused")
	}
	if VerifyInternal(sign(t, "k-1", "n-1", ts), "n-1", ts, now) {
		t.Fatal("a replayed nonce was accepted")
	}
	if VerifyInternal(sign(t, "other", "n-2", ts), "n-2", ts, now) {
		t.Fatal("a signature under another key was accepted")
	}
	old := fmt.Sprintf("%d", now.Add(-10*time.Minute).UnixMilli())
	if VerifyInternal(sign(t, "k-1", "n-3", old), "n-3", old, now) {
		t.Fatal("a stale call was accepted")
	}
	t.Setenv("HMAC_KEY", "")
	if VerifyInternal(sign(t, "", "n-4", ts), "n-4", ts, now) {
		t.Fatal("with no HMAC_KEY nothing may verify")
	}
}

package auth

import (
	"testing"

	"github.com/awesome-goose/goose/modules/router"
	"github.com/awesome-goose/goose/types"
)

type dummy struct{}

func TestSecureGuardsEveryRouteButTheOpenOnes(t *testing.T) {
	r := Secure(router.Resource("thing", dummy{}).All(), "List", "Get")
	got := map[string]bool{}
	for _, c := range r.Children {
		name := c.Handler.([]any)[1].(string)
		got[name] = len(c.Middlewares) > 0
	}
	for name, guarded := range map[string]bool{"List": false, "Get": false, "Create": true, "Update": true, "Delete": true} {
		if got[name] != guarded {
			t.Errorf("%s guarded = %v, want %v", name, got[name], guarded)
		}
	}
}

func TestSessionFailsClosedWithoutASecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "")
	if sessionSecret() == "" || sessionSecret() == "changeme" {
		t.Fatalf("an unset JWT_SECRET must not fall back to a known value")
	}
	t.Setenv("JWT_SECRET", "abc")
	if sessionSecret() != "abc" {
		t.Fatalf("the secret must come from JWT_SECRET")
	}
	var _ []types.Middleware = Session()
}

func TestRevocationHookIsAskedAboutEveryToken(t *testing.T) {
	SetSessionRevoked(func(c map[string]any) bool { return c["sub"] == "gone" })
	defer SetSessionRevoked(nil)
	if !sessionRevoked(map[string]any{"sub": "gone"}) || sessionRevoked(map[string]any{"sub": "ok"}) {
		t.Fatal("the registered hook was not used")
	}
}

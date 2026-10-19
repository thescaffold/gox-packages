package auth

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync"

	"github.com/awesome-goose/goose/types"
)

// Session is the default guard for a module's routes: a verified Bearer session token, signed
// with the host's JWT_SECRET, that no revocation hook has ended. Routes that carry no
// authentication of their own answer anonymous callers (DECISIONS S-01), so a module declares the
// routes that must work without a session (sign-in, a webhook with its own credential) and
// everything else uses this.
//
// With no JWT_SECRET it fails closed: it verifies against a random value no token can match,
// rather than against a well-known default.
func Session() []types.Middleware {
	return []types.Middleware{&AuthMiddleware{SecretFn: sessionSecret, Revoked: sessionRevoked}}
}

var (
	failClosed = func() string {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		return hex.EncodeToString(b)
	}()

	revokedMu   sync.RWMutex
	revokedHook func(claims map[string]any) bool
)

func sessionSecret() string {
	if v := os.Getenv("JWT_SECRET"); v != "" {
		return v
	}
	return failClosed
}

// SetSessionRevoked registers the deployment's check for sessions ended after their token was
// issued (sign out everywhere, account closure). Identity registers it; a host without identity
// may leave it unset.
func SetSessionRevoked(fn func(claims map[string]any) bool) {
	revokedMu.Lock()
	revokedHook = fn
	revokedMu.Unlock()
}

func sessionRevoked(claims map[string]any) bool {
	revokedMu.RLock()
	fn := revokedHook
	revokedMu.RUnlock()
	return fn != nil && fn(claims)
}

// Secure puts Session on every route of a resource (or any route with children), except the
// handler methods named in public, which stay open: `Secure(router.Resource("list", C{}).All(),
// "List", "Get")` leaves the reads public and the writes behind a session.
func Secure(route types.Route, public ...string) types.Route {
	open := map[string]bool{}
	for _, p := range public {
		open[p] = true
	}
	secured := make(types.Routes, 0, len(route.Children))
	for _, child := range route.Children {
		if tuple, ok := child.Handler.([]any); ok && len(tuple) >= 2 {
			if name, _ := tuple[1].(string); open[name] {
				secured = append(secured, child)
				continue
			}
		}
		child.Middlewares = append(append([]types.Middleware{}, Session()...), child.Middlewares...)
		secured = append(secured, child)
	}
	route.Children = secured
	return route
}

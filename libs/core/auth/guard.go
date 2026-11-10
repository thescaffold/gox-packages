package auth

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/security"
)

// Session only proves who is calling. These guards also decide what the caller may reach, so a
// module mounted with no middleware of its own (every scaffold app in every product) is safe:
//
//   - Caller: a verified session plus its context (FromClaims), for routes that act on the
//     caller's own data. Handlers read the user, client and workspace from the token, never
//     from the body or x-ntx-* headers.
//   - Admin: a platform admin (ROOT_USER_ID, or a token holding the root or global-admin
//     role), for platform-wide tables and consoles: queues, crons, pipelines, catalogs.
//   - Internal: a service call signed by core/http.Client.Internal with the host's HMAC_KEY,
//     or a platform admin. For the hooks other services call (register a cron, a health
//     check, a flag).
//
// A workspace owner is not a platform admin: in a self-serve product every person owns a
// workspace, so a workspace role says nothing about platform-wide data.

// Caller is Session plus FromClaims: a verified session whose claims fill the NTXContext.
func Caller() []types.Middleware {
	return append(Session(), &FromClaims{})
}

// platformAdminRoles are the role names that administer the whole platform.
var platformAdminRoles = map[string]bool{"root": true, "global-admin": true}

// PlatformAdmin reports whether a verified caller administers the platform: the ROOT_USER_ID
// account, or a token holding the root or global-admin role (any case, _ or -).
func PlatformAdmin(ntx ntxctx.NTXContext) bool {
	if ntx.UserID == "" {
		return false
	}
	if root := os.Getenv("ROOT_USER_ID"); root != "" && ntx.UserID == root {
		return true
	}
	for _, r := range ntx.Roles {
		if name, ok := r.(string); ok {
			if platformAdminRoles[strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "_", "-")] {
				return true
			}
		}
	}
	return false
}

// AdminGuard refuses (403) a caller who is not a platform admin. It reads the NTXContext, so it
// runs after Session and FromClaims; Admin() is the whole chain.
type AdminGuard struct{}

var _ types.Middleware = (*AdminGuard)(nil)

func (g *AdminGuard) Handle(ctx types.Context) error {
	if PlatformAdmin(ntxctx.Get(ctx)) {
		return nil
	}
	return writeForbidden(ctx, "this route needs a platform administrator")
}

// Admin is Caller plus AdminGuard.
func Admin() []types.Middleware {
	return append(Caller(), &AdminGuard{})
}

// Guard puts mws in front of every route of a resource (or any route with children), except
// the handler methods named in public, which keep only their own middleware.
func Guard(route types.Route, mws []types.Middleware, public ...string) types.Route {
	open := map[string]bool{}
	for _, p := range public {
		open[p] = true
	}
	return guardEach(route, func(name string) []types.Middleware {
		if open[name] {
			return nil
		}
		return mws
	})
}

// SecureCaller is Secure with the caller's context: every route needs a session, and handlers
// (and crud's WorkspaceScoped / OwnerScoped) see the verified user and workspace.
func SecureCaller(route types.Route, public ...string) types.Route {
	return Guard(route, Caller(), public...)
}

// SecureAdmin puts every route of a resource, except the handlers named in public, behind a
// platform admin.
func SecureAdmin(route types.Route, public ...string) types.Route {
	return Guard(route, Admin(), public...)
}

// readHandlers are the crud handlers that change nothing.
var readHandlers = map[string]bool{"List": true, "Get": true, "Metrics": true, "FindByType": true, "FindByIds": true, "FindRelatives": true}

// AdminWrites lets any verified caller read a resource (scoped by its crud config) and keeps
// every write for a platform admin. For records a person may see but must never write
// themselves: payments, balances, plans, audit logs, a catalog.
func AdminWrites(route types.Route) types.Route {
	return guardEach(route, func(name string) []types.Middleware {
		if readHandlers[name] {
			return Caller()
		}
		return Admin()
	})
}

func guardEach(route types.Route, pick func(handler string) []types.Middleware) types.Route {
	secured := make(types.Routes, 0, len(route.Children))
	for _, child := range route.Children {
		name := ""
		if tuple, ok := child.Handler.([]any); ok && len(tuple) >= 2 {
			name, _ = tuple[1].(string)
		}
		if mws := pick(name); len(mws) > 0 {
			child.Middlewares = append(append([]types.Middleware{}, mws...), child.Middlewares...)
		}
		secured = append(secured, child)
	}
	route.Children = secured
	return route
}

// InternalTolerance is how far an internal call's x-ntx-timestamp may be from now.
const InternalTolerance = 5 * time.Minute

// InternalGuard admits a service call signed the way core/http.Client.Internal signs it
// (x-ntx-nonce, x-ntx-timestamp in Unix ms, authorization: hmac <hex HMAC-SHA256 of
// {"nonce","timestamp"}> under HMAC_KEY), within InternalTolerance and with a nonce not seen
// before, or else a platform admin's session. Anything else is 401 (no credential) or 403.
//
// Without HMAC_KEY only the admin path is open. The signature covers the nonce and timestamp,
// not the body, so it proves the caller holds the key; keep HMAC_KEY to services you trust.
type InternalGuard struct{}

var _ types.Middleware = (*InternalGuard)(nil)

func (g *InternalGuard) Handle(ctx types.Context) error {
	headers := ctx.Request().Headers()
	if sig, ok := strings.CutPrefix(headerValue(headers, "authorization"), "hmac "); ok {
		if VerifyInternal(strings.TrimSpace(sig), headerValue(headers, "x-ntx-nonce"), headerValue(headers, "x-ntx-timestamp"), time.Now()) {
			return nil
		}
		return writeUnauthorized(ctx)
	}
	for _, m := range Admin() {
		if err := m.Handle(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Internal is the chain for service-to-service hooks: InternalGuard.
func Internal() []types.Middleware {
	return []types.Middleware{&InternalGuard{}}
}

var (
	noncesMu sync.Mutex
	nonces   = map[string]time.Time{}
)

// VerifyInternal checks an internal call's signature, freshness and nonce. A nonce is accepted
// once per tolerance window.
func VerifyInternal(signature, nonce, timestamp string, now time.Time) bool {
	key := os.Getenv("HMAC_KEY")
	if key == "" || signature == "" || nonce == "" || timestamp == "" {
		return false
	}
	ms, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	at := time.UnixMilli(ms)
	if at.Before(now.Add(-InternalTolerance)) || at.After(now.Add(InternalTolerance)) {
		return false
	}
	if !security.CompareHmac(signature, map[string]any{"nonce": nonce, "timestamp": timestamp}, "sha256", key) {
		return false
	}
	noncesMu.Lock()
	defer noncesMu.Unlock()
	if len(nonces) > 4096 {
		for n, seen := range nonces {
			if now.Sub(seen) > 2*InternalTolerance {
				delete(nonces, n)
			}
		}
	}
	if _, replay := nonces[nonce]; replay {
		return false
	}
	nonces[nonce] = now
	return true
}

package auth

import (
	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/utils"
)

// FromClaims derives a trusted NTXContext from AuthMiddleware's already-
// verified JWT claims — UserID, ClientID, WorkspaceID/Workspace, Roles and
// Permissions — and stores it the way the `context:"ntx"` DTO tag expects
// (ntxctx.Set/Get). Run it after AuthMiddleware in a route's Middlewares.
//
// This is the safe alternative to ntxctx.Middleware (context/middleware.go),
// which parses the same fields straight off unauthenticated, client-supplied
// x-ntx-* request headers — fine for trusted service-to-service calls, but
// never safe to mount on a route a public client can reach: a caller can set
// x-ntx-workspace to any value it likes with no proof it belongs there. PLAN
// (Origine) M1-02 found this the hard way: no route in the apps this session
// had built (systems, tasks, audit, autonomy, ledger) applied *any*
// middleware at all, so every one of them read an always-nil ctx.Workspace —
// their own "WorkspaceId only from context, never the client body" create
// hooks (M1-08's own fix) silently never fired, and a plain client-supplied
// workspaceId in the request body was accepted completely unchecked. Wiring
// AuthMiddleware + FromClaims onto a mount (via the new router.Mount
// middlewares parameter) closes that: Workspace then comes only from a
// value the signing server itself put in a token it verified.
//
// A generalization of identity/app/auth's own claimsToContext (which
// predates this and only derives UserID/ClientID) — that one stays as is,
// this is for every other app that needs the full context, not just one
// route pair.
type FromClaims struct{}

var _ types.Middleware = (*FromClaims)(nil)

func (m *FromClaims) Handle(ctx types.Context) error {
	claims := GetClaims(ctx)
	ntx := ntxctx.NTXContext{}
	if claims != nil {
		if v, ok := claims["sub"].(string); ok {
			ntx.UserID = v
		}
		if v, ok := claims["clientId"].(string); ok {
			ntx.ClientID = v
		}
		if v, ok := claims["workspaceId"].(string); ok && v != "" {
			ntx.WorkspaceID = v
			ntx.Workspace = utils.KeyValue{"id": v}
		}
		if v, ok := claims["roles"].([]any); ok {
			ntx.Roles = v
		}
		if v, ok := claims["permissions"].([]any); ok {
			ntx.Permissions = v
		}
	}
	ntxctx.Set(ctx, ntx)
	return nil
}

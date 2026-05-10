package auth

import (
	"errors"

	"github.com/awesome-goose/goose/types"
)

// errForbidden is the sentinel returned when status guard rejects a request.
var errForbidden = errors.New("forbidden")

// StatusResolver returns the activity status of the authenticated principal.
// Typical values: "active", "suspended", "banned", "pending". Return an error
// if the principal cannot be resolved (e.g. claims missing user id).
type StatusResolver func(claims map[string]any) (status string, err error)

// StatusGuard rejects requests whose authenticated principal is not in the
// AllowedStatuses set. Defaults to {"active"} when AllowedStatuses is empty.
//
// Place this middleware AFTER AuthMiddleware so claims are populated.
// Mirrors ntx-packages/libs/core/src/guards/status.guard.ts.
type StatusGuard struct {
	Resolver        StatusResolver
	AllowedStatuses []string
	UnauthorizedMsg string
}

var _ types.Middleware = (*StatusGuard)(nil)

func (g *StatusGuard) Handle(ctx types.Context) error {
	claims := GetClaims(ctx)
	if claims == nil {
		return writeUnauthorized(ctx)
	}
	if g.Resolver == nil {
		return writeForbidden(ctx, "status resolver not configured")
	}

	status, err := g.Resolver(claims)
	if err != nil {
		return writeForbidden(ctx, "could not verify account status")
	}

	allowed := g.AllowedStatuses
	if len(allowed) == 0 {
		allowed = []string{"active"}
	}
	for _, s := range allowed {
		if s == status {
			return nil
		}
	}

	msg := g.UnauthorizedMsg
	if msg == "" {
		msg = "account is not active"
	}
	return writeForbidden(ctx, msg)
}

package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages-core/response"
)

// AuthMiddleware verifies a Bearer JWT on every request.
// The JWT secret must be injected at construction time.
// On success, the decoded claims map is stored in the goose context under "auth".
// On failure, the 401 Envelope is written to the response and a sentinel error
// is returned to stop the middleware chain.
type AuthMiddleware struct {
	Secret string
}

// claimsKey is the context key under which decoded JWT claims are stored.
const claimsKey = "auth"

// errUnauthorized is the sentinel returned when auth fails.
var errUnauthorized = errors.New("unauthorized")

// Compile-time check that AuthMiddleware implements types.Middleware.
var _ types.Middleware = (*AuthMiddleware)(nil)

func (m *AuthMiddleware) Handle(ctx types.Context) error {
	header := ""
	if headers := ctx.Request().Headers(); headers != nil {
		if vs, ok := headers["authorization"]; ok && len(vs) > 0 {
			header = vs[0]
		}
	}

	if !strings.HasPrefix(header, "Bearer ") {
		return writeUnauthorized(ctx)
	}

	tokenStr := strings.TrimPrefix(header, "Bearer ")
	claims, err := Verify(tokenStr, m.Secret)
	if err != nil {
		return writeUnauthorized(ctx)
	}

	ctx.SetValue(claimsKey, claims)
	return nil
}

// GetClaims retrieves the decoded JWT claims from the goose context.
// Returns nil if the middleware has not populated them.
func GetClaims(ctx types.Context) map[string]any {
	v := ctx.GetValue(claimsKey)
	if v == nil {
		return nil
	}
	claims, _ := v.(map[string]any)
	return claims
}

func writeUnauthorized(ctx types.Context) error {
	env := response.Unauthorized("Unauthorized", "missing or invalid token").Data()
	body, _ := json.Marshal(env)
	_ = ctx.Response().Write(types.SerialTypeObject, body, http.StatusUnauthorized)
	return errUnauthorized
}

package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages/libs/core/response"
)

// AuthMiddleware verifies a Bearer JWT on every request.
// The JWT secret must be injected at construction time.
// On success, the decoded claims map is stored in the goose context under "auth".
// On failure, the 401 Envelope is written to the response and a sentinel error
// is returned to stop the middleware chain.
type AuthMiddleware struct {
	Secret string
	// SecretFn, when set, supplies the secret on every request instead of
	// Secret. Use it when the secret comes from the environment: a middleware
	// built in a package-level var is constructed before the host has loaded
	// its .env, so a fixed Secret would be the unconfigured default.
	SecretFn func() string
	// Revoked, when set, is asked about every token whose signature and
	// expiry are valid; true refuses it. It is how a deployment ends sessions
	// that a stateless token would otherwise outlive (sign-out everywhere,
	// account closure). Nil keeps the old behaviour exactly.
	Revoked func(claims map[string]any) bool
}

func (m *AuthMiddleware) secret() string {
	if m.SecretFn != nil {
		return m.SecretFn()
	}
	return m.Secret
}

// claimsKey is the context key under which decoded JWT claims are stored.
const claimsKey = "auth"

// errUnauthorized is the sentinel returned when auth fails.
var errUnauthorized = errors.New("unauthorized")

// Compile-time check that AuthMiddleware implements types.Middleware.
var _ types.Middleware = (*AuthMiddleware)(nil)

func (m *AuthMiddleware) Handle(ctx types.Context) error {
	header := headerValue(ctx.Request().Headers(), "authorization")

	// passport-jwt's fromAuthHeaderAsBearerToken matches the "bearer" scheme
	// case-insensitively, so accept any capitalisation of the prefix.
	const bearerPrefix = "bearer "
	if len(header) < len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return writeUnauthorized(ctx)
	}

	tokenStr := strings.TrimSpace(header[len(bearerPrefix):])
	claims, err := Verify(tokenStr, m.secret())
	if err != nil {
		return writeUnauthorized(ctx)
	}

	if m.Revoked != nil && m.Revoked(claims) {
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

// headerValue looks up name case-insensitively in headers. Real HTTP
// platforms (net/http-backed — platforms/api, /web, /spa) always store
// header keys in their canonical form (textproto.CanonicalMIMEHeaderKey,
// e.g. "Authorization"), but this must also tolerate whatever casing a
// test's mock context or another platform (cli, etc.) happens to use — a
// straight map lookup on one fixed casing missed real requests entirely
// (see TestAuthMiddleware_CanonicalHeaderCasing_StoresClaims).
func headerValue(headers map[string][]string, name string) string {
	for k, vs := range headers {
		if strings.EqualFold(k, name) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

func writeUnauthorized(ctx types.Context) error {
	env := response.Unauthorized("Unauthorized", "missing or invalid token").Data()
	body, _ := json.Marshal(env)
	_ = ctx.Response().Write(types.SerialTypeObject, body, http.StatusUnauthorized)
	return errUnauthorized
}

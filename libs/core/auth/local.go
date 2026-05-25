package auth

import (
	"encoding/json"
	"net/http"

	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages/libs/core/response"
)

// LocalCredentials is the body shape for a local-strategy login request.
type LocalCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LocalVerifier validates a username+password pair, returning the resolved
// principal (user record / claims) on success or an error on failure.
//
// Implementations typically: lookup the user by username, then bcrypt-compare
// the password. Return nil + a non-nil error to fail authentication.
type LocalVerifier func(username, password string) (principal map[string]any, err error)

// LocalGuard authenticates a request via a JSON body of LocalCredentials.
// On success the principal is stored under "auth" (same key as JwtGuard /
// AuthMiddleware), so downstream handlers see a uniform auth context.
//
// Mirrors ntx-packages/libs/core/src/guards/local.guard.ts.
type LocalGuard struct {
	Verifier LocalVerifier
}

var _ types.Middleware = (*LocalGuard)(nil)

func (g *LocalGuard) Handle(ctx types.Context) error {
	if g.Verifier == nil {
		return writeUnauthorized(ctx)
	}

	raw, err := ctx.Request().Body()
	if err != nil || len(raw) == 0 {
		return writeUnauthorized(ctx)
	}
	var creds LocalCredentials
	if err := json.Unmarshal(raw, &creds); err != nil {
		return writeUnauthorized(ctx)
	}
	if creds.Username == "" || creds.Password == "" {
		return writeUnauthorized(ctx)
	}

	principal, err := g.Verifier(creds.Username, creds.Password)
	if err != nil || principal == nil {
		return writeUnauthorized(ctx)
	}
	ctx.SetValue(claimsKey, principal)
	return nil
}

// writeForbidden writes a 403 envelope, mirroring writeUnauthorized.
func writeForbidden(ctx types.Context, message string) error {
	env := response.Forbidden("Forbidden", message).Data()
	body, _ := json.Marshal(env)
	_ = ctx.Response().Write(types.SerialTypeObject, body, http.StatusForbidden)
	return errForbidden
}

// writeBadRequest writes a 400 envelope carrying separate title/message fields,
// mirroring TS error(title, message) which throws an HttpException with
// HttpStatus.BAD_REQUEST and body {status:'error', title, message, data:null}.
func writeBadRequest(ctx types.Context, title, message string) error {
	env := response.BadRequest(title, message).Data()
	body, _ := json.Marshal(env)
	_ = ctx.Response().Write(types.SerialTypeObject, body, http.StatusBadRequest)
	return errForbidden
}

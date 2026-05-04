package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Sign creates a HS256 JWT from the given claims map.
// All entries in claims are added as custom claims; "exp" is set from expiry.
func Sign(claims map[string]any, secret string, expiry time.Duration) (string, error) {
	mc := jwt.MapClaims{}
	for k, v := range claims {
		mc[k] = v
	}
	if expiry != 0 {
		mc["exp"] = time.Now().Add(expiry).Unix()
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, mc)
	return token.SignedString([]byte(secret))
}

// Verify parses and validates a HS256 JWT, returning the claims map.
// Returns an error if the token is invalid, expired, or uses a different algorithm.
func Verify(tokenStr, secret string) (map[string]any, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	mc, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	out := make(map[string]any, len(mc))
	for k, v := range mc {
		out[k] = v
	}
	return out, nil
}

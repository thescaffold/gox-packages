package objectstore

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Drivers without a native presigned URL (fs, postgres) return an
// Origine-signed one: /api/blobs/dl/<token>, where the token is an HMAC-signed
// claim set the download route verifies (TRD §6.10). The S3 driver returns a
// native URL instead; callers never see the difference.

const (
	// DefaultPresignTTL and MaxPresignTTL: 5–15 minutes.
	DefaultPresignTTL = 10 * time.Minute
	MinPresignTTL     = 5 * time.Minute
	MaxPresignTTL     = 15 * time.Minute

	// DownloadPathPrefix is where the download route is mounted.
	DownloadPathPrefix = "/api/blobs/dl/"
)

// DownloadClaims is what a download token carries.
type DownloadClaims struct {
	Key       string    `json:"k"`
	Expires   time.Time `json:"e"`
	Filename  string    `json:"f,omitempty"`
	SingleUse bool      `json:"s,omitempty"`
	// ID is unique per token, so a single-use store can record it as spent.
	ID string `json:"i"`
}

var (
	ErrBadToken     = errors.New("objectstore: malformed or forged download token")
	ErrTokenExpired = errors.New("objectstore: download token expired")
)

// ClampTTL applies the 5–15 minute bounds (0 means the default).
func ClampTTL(ttl time.Duration) time.Duration {
	switch {
	case ttl <= 0:
		return DefaultPresignTTL
	case ttl < MinPresignTTL:
		return MinPresignTTL
	case ttl > MaxPresignTTL:
		return MaxPresignTTL
	}
	return ttl
}

func tokenMAC(payload string, secret []byte) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(payload))
	return h.Sum(nil)
}

// SignDownload builds the URL path for key.
func SignDownload(key string, opts PresignOptions, secret []byte, now time.Time) (string, error) {
	if len(secret) < 16 {
		return "", errors.New("objectstore: presign secret must be at least 16 bytes")
	}
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	claims := DownloadClaims{
		Key: key, Expires: now.Add(ClampTTL(opts.TTL)).UTC(), Filename: opts.Filename,
		SingleUse: opts.SingleUse, ID: base64.RawURLEncoding.EncodeToString(id),
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(raw)
	return DownloadPathPrefix + p + "." + base64.RawURLEncoding.EncodeToString(tokenMAC(p, secret)), nil
}

// VerifyDownload checks a token (or a full URL path ending in one) and returns
// its claims. It does not enforce single use — that needs state, which the
// serving route owns.
func VerifyDownload(tokenOrPath string, secret []byte, now time.Time) (DownloadClaims, error) {
	tok := strings.TrimPrefix(tokenOrPath, DownloadPathPrefix)
	parts := strings.Split(tok, ".")
	if len(parts) != 2 {
		return DownloadClaims{}, ErrBadToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(sig, tokenMAC(parts[0], secret)) {
		return DownloadClaims{}, ErrBadToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return DownloadClaims{}, ErrBadToken
	}
	var c DownloadClaims
	if err := json.Unmarshal(raw, &c); err != nil || ValidateKey(c.Key) != nil {
		return DownloadClaims{}, ErrBadToken
	}
	if !now.Before(c.Expires) {
		return DownloadClaims{}, ErrTokenExpired
	}
	return c, nil
}

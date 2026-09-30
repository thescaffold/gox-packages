package objectstore

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxKeyLength bounds a key.
const MaxKeyLength = 1024

// ValidateKey enforces the rules every driver shares: non-empty, valid UTF-8,
// no NUL or backslash or control characters, no leading or trailing slash, no
// empty, "." or ".." segments (so a key can never climb out of its prefix, on
// a filesystem or anywhere else).
func ValidateKey(key string) error {
	if key == "" || len(key) > MaxKeyLength || !utf8.ValidString(key) {
		return fmt.Errorf("%w: empty, too long or not UTF-8", ErrInvalidKey)
	}
	if strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return fmt.Errorf("%w: must not start or end with '/'", ErrInvalidKey)
	}
	for _, r := range key {
		if r == '\\' || r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: contains a control character or backslash", ErrInvalidKey)
		}
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: empty, '.' or '..' path segment", ErrInvalidKey)
		}
	}
	return nil
}

// ValidatePrefix is ValidateKey for a List prefix, which may end with '/'.
func ValidatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	return ValidateKey(strings.TrimSuffix(prefix, "/"))
}

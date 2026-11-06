package kms

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

// Local is a KMS whose key encryption keys come from configuration. Several
// keys may be configured so the current one can rotate: data keys wrapped by
// an older key still unwrap, and new wraps use the current key.
type Local struct {
	keys    map[string][]byte
	current string
}

var _ KMS = (*Local)(nil)

// NewLocal builds a Local KMS from key id → 32-byte key, wrapping with current.
func NewLocal(keys map[string][]byte, current string) (*Local, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: no keys", ErrConfig)
	}
	for id, k := range keys {
		if id == "" || strings.ContainsAny(id, ":, ") {
			return nil, fmt.Errorf("%w: key id %q", ErrConfig, id)
		}
		if len(k) != 32 {
			return nil, fmt.Errorf("%w: key %q is %d bytes, want 32", ErrConfig, id, len(k))
		}
	}
	if _, ok := keys[current]; !ok {
		return nil, fmt.Errorf("%w: current key %q is not configured", ErrConfig, current)
	}
	cp := make(map[string][]byte, len(keys))
	for id, k := range keys {
		cp[id] = append([]byte(nil), k...)
	}
	return &Local{keys: cp, current: current}, nil
}

func (l *Local) Wrap(_ context.Context, dek, aad []byte) (string, []byte, error) {
	w, err := Seal(l.keys[l.current], dek, aad)
	return "local:" + l.current, w, err
}

func (l *Local) Unwrap(_ context.Context, keyID string, wrapped, aad []byte) ([]byte, error) {
	id, ok := strings.CutPrefix(keyID, "local:")
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a local key", ErrUnknownKey, keyID)
	}
	k, ok := l.keys[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKey, keyID)
	}
	return Open(k, wrapped, aad)
}

// FromEnv builds a KMS from the environment:
//
//	KMS_DRIVER        local (default)
//	KMS_LOCAL_KEYS    id:base64key[,id:base64key…]  (each key 32 bytes)
//	KMS_LOCAL_CURRENT the id new wraps use (default: the last one listed)
//
// With no keys configured it returns nil and no error: the caller decides
// whether running without a KMS is acceptable.
func FromEnv(getenv func(string) string) (KMS, error) {
	switch d := strings.ToLower(strings.TrimSpace(getenv("KMS_DRIVER"))); d {
	case "", "local":
	default:
		return nil, fmt.Errorf("%w: unknown KMS_DRIVER %q", ErrConfig, d)
	}
	raw := strings.TrimSpace(getenv("KMS_LOCAL_KEYS"))
	if raw == "" {
		return nil, nil
	}
	keys := map[string][]byte{}
	last := ""
	for _, part := range strings.Split(raw, ",") {
		id, b64, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok {
			return nil, fmt.Errorf("%w: KMS_LOCAL_KEYS entries are id:base64key", ErrConfig)
		}
		k, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("%w: key %q is not base64", ErrConfig, id)
		}
		keys[id] = k
		last = id
	}
	current := strings.TrimSpace(getenv("KMS_LOCAL_CURRENT"))
	if current == "" {
		current = last
	}
	return NewLocal(keys, current)
}

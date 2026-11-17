package kms

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

// Rewrapper is a KMS that can move a wrapped data key to its current key
// encryption key itself. Transit does it inside the server, so the data key
// is never seen; use the Rewrap function, which falls back to Unwrap and Wrap.
type Rewrapper interface {
	Rewrap(ctx context.Context, keyID string, wrapped, aad []byte) (newKeyID string, rewrapped []byte, err error)
}

// Rewrap moves a wrapped data key to k's current key encryption key, keeping
// its aad: after a rotation, stored keys are rewrapped so the older key (or
// key version) can be retired. Through a Rewrapper when k is one; otherwise
// the data key is unwrapped and wrapped again in memory.
func Rewrap(ctx context.Context, k KMS, keyID string, wrapped, aad []byte) (string, []byte, error) {
	if r, ok := k.(Rewrapper); ok {
		return r.Rewrap(ctx, keyID, wrapped, aad)
	}
	return rewrapThrough(ctx, k, keyID, wrapped, aad)
}

func rewrapThrough(ctx context.Context, k KMS, keyID string, wrapped, aad []byte) (string, []byte, error) {
	dek, err := k.Unwrap(ctx, keyID, wrapped, aad)
	if err != nil {
		return "", nil, err
	}
	defer clear(dek)
	return k.Wrap(ctx, dek, aad)
}

// Chain wraps with its first KMS and unwraps with whichever of them knows the
// key id, so an install can move from one driver to another (Local to
// transit, say) and keep reading what the old one wrapped until it is
// rewrapped.
type Chain []KMS

var (
	_ KMS       = Chain(nil)
	_ Rewrapper = Chain(nil)
)

func (c Chain) Wrap(ctx context.Context, dek, aad []byte) (string, []byte, error) {
	if len(c) == 0 {
		return "", nil, fmt.Errorf("%w: an empty chain", ErrConfig)
	}
	return c[0].Wrap(ctx, dek, aad)
}

func (c Chain) Unwrap(ctx context.Context, keyID string, wrapped, aad []byte) ([]byte, error) {
	err := fmt.Errorf("%w: %q", ErrUnknownKey, keyID)
	for _, k := range c {
		dek, e := k.Unwrap(ctx, keyID, wrapped, aad)
		if e == nil {
			return dek, nil
		}
		if !errors.Is(e, ErrUnknownKey) {
			return nil, e
		}
		err = e
	}
	return nil, err
}

// Rewrap rewraps inside the first KMS when it is a Rewrapper (a key it
// wrapped, or a key it can read), else unwraps with the chain and wraps with
// the first.
func (c Chain) Rewrap(ctx context.Context, keyID string, wrapped, aad []byte) (string, []byte, error) {
	if len(c) == 0 {
		return "", nil, fmt.Errorf("%w: an empty chain", ErrConfig)
	}
	if t, ok := c[0].(*Transit); ok && keyID == t.KeyID() {
		return t.Rewrap(ctx, keyID, wrapped, aad)
	}
	return rewrapThrough(ctx, c, keyID, wrapped, aad)
}

// Probe wraps a fresh data key and unwraps it, then checks that other
// associated data does not unwrap it: the round trip `admin check` runs.
// It returns the key id the wrap used.
func Probe(ctx context.Context, k KMS) (string, error) {
	dek, err := NewDataKey()
	if err != nil {
		return "", err
	}
	aad := []byte("kms-probe")
	id, wrapped, err := k.Wrap(ctx, dek, aad)
	if err != nil {
		return "", fmt.Errorf("a wrap failed: %w", err)
	}
	got, err := k.Unwrap(ctx, id, wrapped, aad)
	if err != nil {
		return id, fmt.Errorf("an unwrap failed: %w", err)
	}
	if !bytes.Equal(got, dek) {
		return id, fmt.Errorf("%w: the unwrapped key differs from the one wrapped", ErrDecrypt)
	}
	if _, err := k.Unwrap(ctx, id, wrapped, []byte("kms-probe-other")); err == nil {
		return id, fmt.Errorf("%w: a key unwrapped with other associated data, so wraps are not bound to their context", ErrConfig)
	} else if !errors.Is(err, ErrDecrypt) {
		return id, fmt.Errorf("an unwrap with other associated data failed for another reason: %w", err)
	}
	return id, nil
}

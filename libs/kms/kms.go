// Package kms is the key-management port for envelope encryption: a data key
// (DEK) encrypts values, and a key-encryption key held by a KMS wraps the DEK.
// Only wrapped DEKs are stored; losing the database alone never exposes a key.
//
// Drivers: Local (keys from configuration, for development and single-cell
// installs). Cloud KMS and OpenBao transit drivers implement the same
// interface.
package kms

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// Errors (use errors.Is).
var (
	ErrUnknownKey = errors.New("kms: unknown key")
	ErrDecrypt    = errors.New("kms: cannot decrypt (wrong key, wrong context, or modified data)")
	ErrConfig     = errors.New("kms: invalid configuration")
)

// KMS wraps and unwraps data keys. aad binds a wrapped key to its context (a
// workspace id, say): unwrapping with different aad fails.
type KMS interface {
	// Wrap encrypts a data key under the current key encryption key and returns
	// that key's id, which Unwrap needs.
	Wrap(ctx context.Context, dek, aad []byte) (keyID string, wrapped []byte, err error)
	// Unwrap decrypts a data key wrapped by Wrap with keyID.
	Unwrap(ctx context.Context, keyID string, wrapped, aad []byte) ([]byte, error)
}

// NewDataKey returns a fresh 256-bit data key.
func NewDataKey() ([]byte, error) {
	k := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, k); err != nil {
		return nil, err
	}
	return k, nil
}

// Seal encrypts plaintext with a 256-bit key using AES-GCM, binding aad.
// Output: nonce || ciphertext+tag.
func Seal(key, plaintext, aad []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

// Open reverses Seal. Any change to the data, the key or aad is ErrDecrypt.
func Open(key, sealed, aad []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrDecrypt
	}
	n := gcm.NonceSize()
	out, err := gcm.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: keys are 32 bytes, got %d", ErrConfig, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

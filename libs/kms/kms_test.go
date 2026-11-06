package kms

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestSealOpen(t *testing.T) {
	k := key(1)
	s, err := Seal(k, []byte("hunter2"), []byte("item-1:v3"))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := Open(k, s, []byte("item-1:v3")); err != nil || string(out) != "hunter2" {
		t.Fatalf("open: %q %v", out, err)
	}
	if _, err := Open(k, s, []byte("item-2:v3")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("other context opened: %v", err)
	}
	if _, err := Open(key(2), s, []byte("item-1:v3")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("other key opened: %v", err)
	}
	s[len(s)-1] ^= 1
	if _, err := Open(k, s, []byte("item-1:v3")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("modified data opened: %v", err)
	}
	if _, err := Seal([]byte("short"), nil, nil); !errors.Is(err, ErrConfig) {
		t.Fatalf("short key: %v", err)
	}
	a, _ := Seal(k, []byte("x"), nil)
	b, _ := Seal(k, []byte("x"), nil)
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same value must differ (fresh nonce)")
	}
}

func TestLocalWrapUnwrapAndRotation(t *testing.T) {
	ctx := context.Background()
	old, err := NewLocal(map[string][]byte{"k1": key(1)}, "k1")
	if err != nil {
		t.Fatal(err)
	}
	dek, _ := NewDataKey()
	id, wrapped, err := old.Wrap(ctx, dek, []byte("ws-1"))
	if err != nil || id != "local:k1" {
		t.Fatalf("wrap: %q %v", id, err)
	}

	// Rotate: k2 is current, k1 still unwraps.
	rotated, _ := NewLocal(map[string][]byte{"k1": key(1), "k2": key(2)}, "k2")
	if got, err := rotated.Unwrap(ctx, id, wrapped, []byte("ws-1")); err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("unwrap after rotation: %v", err)
	}
	if id2, _, _ := rotated.Wrap(ctx, dek, nil); id2 != "local:k2" {
		t.Fatalf("new wraps use the current key, got %q", id2)
	}
	if _, err := rotated.Unwrap(ctx, id, wrapped, []byte("ws-2")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong workspace unwrapped: %v", err)
	}
	if _, err := rotated.Unwrap(ctx, "local:gone", wrapped, nil); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key: %v", err)
	}
	if _, err := NewLocal(map[string][]byte{"k1": key(1)}, "k9"); !errors.Is(err, ErrConfig) {
		t.Fatalf("missing current key: %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if k, err := FromEnv(env(nil)); k != nil || err != nil {
		t.Fatalf("no keys: %v %v", k, err)
	}
	b1, b2 := base64.StdEncoding.EncodeToString(key(1)), base64.StdEncoding.EncodeToString(key(2))
	k, err := FromEnv(env(map[string]string{"KMS_LOCAL_KEYS": "a:" + b1 + ", b:" + b2}))
	if err != nil {
		t.Fatal(err)
	}
	if id, _, _ := k.Wrap(context.Background(), key(9), nil); id != "local:b" {
		t.Fatalf("default current is the last key, got %q", id)
	}
	if _, err := FromEnv(env(map[string]string{"KMS_DRIVER": "vault"})); !errors.Is(err, ErrConfig) {
		t.Fatalf("unknown driver: %v", err)
	}
	if _, err := FromEnv(env(map[string]string{"KMS_LOCAL_KEYS": "a:" + base64.StdEncoding.EncodeToString([]byte("short"))})); !errors.Is(err, ErrConfig) {
		t.Fatalf("short key: %v", err)
	}
}

package pg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
)

// Envelope encryption (TRD §6.10): every object has its own random 256-bit
// data key. The data key is wrapped (AES-256-GCM) under a per-workspace key
// derived from the master key, and stored on the object row; the master key
// never touches the database. Every chunk is sealed with the data key under
// AAD = object id || seq, so a chunk cannot be moved to another object or
// another position undetected.

func workspaceKey(master []byte, workspaceID string) []byte {
	m := hmac.New(sha256.New, master)
	m.Write([]byte("origine/blobs/workspace-key/v1:" + workspaceID))
	return m.Sum(nil)
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func seal(aead cipher.AEAD, plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plain)+aead.Overhead())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, aad), nil
}

var errTampered = errors.New("pg: stored chunk failed authentication (corrupted or tampered)")

func open(aead cipher.AEAD, blob, aad []byte) ([]byte, error) {
	n := aead.NonceSize()
	if len(blob) < n+aead.Overhead() {
		return nil, errTampered
	}
	plain, err := aead.Open(nil, blob[:n], blob[n:], aad)
	if err != nil {
		return nil, errTampered
	}
	return plain, nil
}

func chunkAAD(objectID string, seq int64) []byte {
	b := make([]byte, 0, len(objectID)+8)
	b = append(b, objectID...)
	return binary.BigEndian.AppendUint64(b, uint64(seq))
}

// wrapKey returns base64(nonce||GCM(dataKey)) bound to the object id.
func wrapKey(master []byte, workspaceID, objectID string, dataKey []byte) (string, error) {
	a, err := newAEAD(workspaceKey(master, workspaceID))
	if err != nil {
		return "", err
	}
	w, err := seal(a, dataKey, []byte(objectID))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(w), nil
}

func unwrapKey(master []byte, workspaceID, objectID, wrapped string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(wrapped)
	if err != nil {
		return nil, errTampered
	}
	a, err := newAEAD(workspaceKey(master, workspaceID))
	if err != nil {
		return nil, err
	}
	return open(a, raw, []byte(objectID))
}

package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"sort"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// ToBase64 encodes a UTF-8 string to base64.
func ToBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// FromBase64 decodes a base64 string to UTF-8.
func FromBase64(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// MustFromBase64 decodes base64, panicking on error. Use only in tests.
func MustFromBase64(s string) string {
	v, err := FromBase64(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Hash bcrypt-hashes text.
func Hash(text string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(text), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// Compare verifies text against a bcrypt hash.
func Compare(text, hashed string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(text)) == nil
}

// Encrypt encrypts text with AES-256-CBC using key.
// Output format: <iv_hex>:<ciphertext_hex>  (mirrors TS implementation)
func Encrypt(text, key string) (string, error) {
	if text == "" {
		return text, nil
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	iv := make([]byte, aes.BlockSize)
	if _, err = io.ReadFull(rand.Reader, iv); err != nil {
		return "", fmt.Errorf("encrypt iv: %w", err)
	}
	padded := pkcs7Pad([]byte(text), aes.BlockSize)
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(padded, padded)
	return hex.EncodeToString(iv) + ":" + hex.EncodeToString(padded), nil
}

// Decrypt decrypts an AES-256-CBC ciphertext produced by Encrypt.
func Decrypt(ciphertext, key string) (string, error) {
	if ciphertext == "" {
		return ciphertext, nil
	}
	parts := strings.SplitN(ciphertext, ":", 2)
	if len(parts) != 2 {
		return "", errors.New("decrypt: invalid ciphertext format")
	}
	iv, err := hex.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decrypt iv: %w", err)
	}
	ct, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decrypt ct: %w", err)
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	if len(ct)%aes.BlockSize != 0 {
		return "", errors.New("decrypt: ciphertext not block-aligned")
	}
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(ct, ct)
	unpadded, err := pkcs7Unpad(ct)
	if err != nil {
		return "", err
	}
	return string(unpadded), nil
}

// GenerateHmac signs payload (JSON-marshalled with sorted keys) using algo and key.
// Supported algos: "sha1", "sha256" (default), "sha384", "sha512" — matches Node's
// crypto.createHmac surface that the TS QuickHttpService relies on.
func GenerateHmac(payload any, algo, key string) (string, error) {
	sorted, err := sortedJSON(payload)
	if err != nil {
		return "", err
	}
	var fn func() hash.Hash
	switch strings.ToLower(algo) {
	case "sha1":
		fn = sha1.New
	case "sha384":
		fn = sha512.New384
	case "sha512":
		fn = sha512.New
	default: // sha256
		fn = sha256.New
	}
	mac := hmac.New(fn, []byte(key))
	mac.Write(sorted)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// CompareHmac verifies a hex HMAC signature against payload.
func CompareHmac(signature string, payload any, algo, key string) bool {
	expected, err := GenerateHmac(payload, algo, key)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(signature), []byte(expected))
}

// CheckSum returns the SHA-256 hex digest of text.
func CheckSum(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// MD5 returns the MD5 hex digest (used for host hashing in Reference).
func MD5(text string) string {
	h := md5.Sum([]byte(text))
	return hex.EncodeToString(h[:])
}

// --- helpers ---

func pkcs7Pad(b []byte, blockSize int) []byte {
	pad := blockSize - len(b)%blockSize
	padded := make([]byte, len(b)+pad)
	copy(padded, b)
	for i := len(b); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	return padded
}

func pkcs7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("pkcs7: empty input")
	}
	pad := int(b[len(b)-1])
	if pad > aes.BlockSize || pad == 0 {
		return nil, fmt.Errorf("pkcs7: invalid padding %d", pad)
	}
	return b[:len(b)-pad], nil
}

func sortedJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	// Unmarshal into ordered map then re-marshal with sorted keys
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		// not an object — marshal as-is
		return b, nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(m))
	for _, k := range keys {
		out[k] = m[k]
	}
	return json.Marshal(out)
}

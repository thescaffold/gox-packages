package security

import (
	"bytes"
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

// gcmVersion prefixes every EncryptGCM envelope. It's what lets DecryptAny
// and ReEncryptToGCM tell a versioned GCM ciphertext apart from legacy
// Encrypt's unversioned AES-CBC output, which never starts with "v1:" (it
// starts directly with a hex IV).
const gcmVersion = "v1"

// EncryptGCM encrypts text with AES-256-GCM (authenticated — unlike Encrypt's
// CBC, a modified ciphertext or tag is rejected, not silently decrypted to
// garbage) using key (32 bytes). Output is a versioned envelope:
// "v1:<nonce_hex>:<sealed_hex>" (PLAN M1-01, TRD U-S1). Encrypt/Decrypt are
// unchanged and still work — this is additive, not a replacement.
func EncryptGCM(text, key string) (string, error) {
	if text == "" {
		return text, nil
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", fmt.Errorf("encryptGCM: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("encryptGCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("encryptGCM nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(text), nil)
	return gcmVersion + ":" + hex.EncodeToString(nonce) + ":" + hex.EncodeToString(sealed), nil
}

// DecryptGCM decrypts and authenticates a ciphertext produced by EncryptGCM.
// A modified ciphertext, a modified/truncated auth tag, or an unversioned
// (legacy CBC) input are all rejected with an error — never silently
// returned as garbage plaintext.
func DecryptGCM(ciphertext, key string) (string, error) {
	if ciphertext == "" {
		return ciphertext, nil
	}
	parts := strings.SplitN(ciphertext, ":", 3)
	if len(parts) != 3 || parts[0] != gcmVersion {
		return "", errors.New("decryptGCM: not a v1 envelope")
	}
	nonce, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decryptGCM nonce: %w", err)
	}
	sealed, err := hex.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("decryptGCM ciphertext: %w", err)
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", fmt.Errorf("decryptGCM: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("decryptGCM: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return "", errors.New("decryptGCM: invalid nonce size")
	}
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decryptGCM: authentication failed: %w", err)
	}
	return string(plain), nil
}

// IsGCMEnvelope reports whether ciphertext is a versioned EncryptGCM output
// rather than legacy Encrypt's unversioned AES-CBC output.
func IsGCMEnvelope(ciphertext string) bool {
	return strings.HasPrefix(ciphertext, gcmVersion+":")
}

// DecryptAny decrypts a value produced by either EncryptGCM (versioned) or
// the legacy Encrypt (unversioned AES-CBC), dispatching on format — so a
// caller reading a column that may hold rows written under either scheme
// doesn't need to know which one a given row used (PLAN M1-01's "old
// ciphertext still decrypts").
func DecryptAny(ciphertext, key string) (string, error) {
	if ciphertext == "" {
		return ciphertext, nil
	}
	if IsGCMEnvelope(ciphertext) {
		return DecryptGCM(ciphertext, key)
	}
	return Decrypt(ciphertext, key)
}

// ReEncryptToGCM is PLAN M1-01's migration helper: given a value that may be
// in either format, returns it re-encrypted as a versioned GCM envelope,
// ready to write back. Idempotent — a value already in GCM format is
// returned unchanged — so it's safe to call unconditionally on every row a
// migration touches rather than needing to check the format first.
func ReEncryptToGCM(ciphertext, key string) (string, error) {
	if ciphertext == "" || IsGCMEnvelope(ciphertext) {
		return ciphertext, nil
	}
	plain, err := Decrypt(ciphertext, key)
	if err != nil {
		return "", fmt.Errorf("reEncryptToGCM decrypt: %w", err)
	}
	return EncryptGCM(plain, key)
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

// sortedJSON mirrors TS `JSON.stringify(sortObject(payload))`. TS sortObject
// only re-orders TOP-LEVEL keys alphabetically — nested objects keep their
// original (insertion) order. Go's json.Marshal of map[string]any sorts ALL
// keys recursively, which diverges from TS for nested payloads. To match TS,
// we marshal preserving inner-key order via json.RawMessage and only sort the
// outermost keys.
//
// Two deviations from Go's default must also be corrected to match TS's
// JSON.stringify output: HTML characters (<, >, &) must NOT be escaped, and
// there must be no trailing newline (which json.Encoder appends).
func sortedJSON(v any) ([]byte, error) {
	b, err := marshalNoEscape(v)
	if err != nil {
		return nil, err
	}
	// Decode the top-level object into ordered raw fields. If v isn't an
	// object, the canonical form is its plain (non-sorted) marshalling.
	var raw map[string]json.RawMessage
	if err = json.Unmarshal(b, &raw); err != nil {
		return b, nil
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := marshalNoEscape(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		// raw[k] preserves the original byte order of nested members
		buf.Write(raw[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// marshalNoEscape JSON-marshals v without HTML escaping and without the trailing
// newline json.Encoder adds, matching JavaScript's JSON.stringify output.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

package tests

import (
	"encoding/json"
	"strings"
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages/libs/core/security"
)

func TestSecurity(t *testing.T) {
	test.NewSuiteRunner(t, &SecuritySuite{}).Run()
}

type SecuritySuite struct {
	test.Suite
}

const testKey = "12345678901234567890123456789012" // 32 bytes

func (s *SecuritySuite) TestBase64_RoundTrip() {
	original := `{"hello":"world"}`
	encoded := security.ToBase64(original)
	decoded, err := security.FromBase64(encoded)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(decoded).ToEqual(original)
}

func (s *SecuritySuite) TestFromBase64_InvalidReturnsError() {
	_, err := security.FromBase64("not-valid-base64!!!")
	s.T.Expect(err).Not().ToBeNil()
}

func (s *SecuritySuite) TestHash_Verify() {
	h, err := security.Hash("mysecret")
	s.T.Expect(err).ToBeNil()
	s.T.Expect(security.Compare("mysecret", h)).ToEqual(true)
	s.T.Expect(security.Compare("wrongpassword", h)).ToEqual(false)
}

func (s *SecuritySuite) TestEncrypt_Decrypt_RoundTrip() {
	plaintext := "hello encrypted world"
	ct, err := security.Encrypt(plaintext, testKey)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(ct).Not().ToEqual(plaintext)

	pt, err := security.Decrypt(ct, testKey)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(pt).ToEqual(plaintext)
}

func (s *SecuritySuite) TestEncrypt_EmptyString() {
	ct, err := security.Encrypt("", testKey)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(ct).ToEqual("")
}

func (s *SecuritySuite) TestEncrypt_Nondeterministic() {
	a, _ := security.Encrypt("same text", testKey)
	b, _ := security.Encrypt("same text", testKey)
	// IV is random so ciphertexts differ
	s.T.Expect(a).Not().ToEqual(b)
}

// EncryptGCM/DecryptGCM (PLAN M1-01, TRD U-S1): core/security's only
// encryption was AES-256-CBC with no authentication tag — malleable
// ciphertext, unacceptable for the GitHub/GitLab tokens and kubeconfigs
// Origine stores. These add an authenticated, versioned envelope alongside
// it (Encrypt/Decrypt keep working — old ciphertext must still decrypt).

func (s *SecuritySuite) TestEncryptGCM_DecryptGCM_RoundTrip() {
	plaintext := "a github access token"
	ct, err := security.EncryptGCM(plaintext, testKey)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(ct).Not().ToEqual(plaintext)

	pt, err := security.DecryptGCM(ct, testKey)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(pt).ToEqual(plaintext)
}

func (s *SecuritySuite) TestEncryptGCM_EmptyString() {
	ct, err := security.EncryptGCM("", testKey)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(ct).ToEqual("")
}

func (s *SecuritySuite) TestEncryptGCM_Nondeterministic() {
	a, _ := security.EncryptGCM("same text", testKey)
	b, _ := security.EncryptGCM("same text", testKey)
	s.T.Expect(a).Not().ToEqual(b)
}

func (s *SecuritySuite) TestEncryptGCM_IsVersioned() {
	ct, _ := security.EncryptGCM("hello", testKey)
	s.T.Expect(strings.HasPrefix(ct, "v1:")).ToEqual(true)
	s.T.Expect(security.IsGCMEnvelope(ct)).ToEqual(true)
}

// TestDecryptGCM_TamperDetection is the actual point of moving to GCM: CBC
// has no authentication tag, so a flipped ciphertext byte just decrypts to
// garbage silently. GCM must refuse it outright.
func (s *SecuritySuite) TestDecryptGCM_TamperDetection() {
	ct, err := security.EncryptGCM("do not modify me", testKey)
	s.T.Expect(err).ToBeNil()

	parts := strings.Split(ct, ":")
	s.T.Expect(len(parts)).ToEqual(3)
	// Flip a hex nibble deep in the ciphertext/tag portion.
	tampered := parts[0] + ":" + parts[1] + ":" + tamperHex(parts[2])

	_, err = security.DecryptGCM(tampered, testKey)
	s.T.Expect(err).Not().ToBeNil()
}

func (s *SecuritySuite) TestDecryptGCM_RejectsUnversionedInput() {
	// A legacy CBC ciphertext (no "v1:" prefix) must be refused by DecryptGCM
	// outright, not misparsed.
	legacy, _ := security.Encrypt("legacy value", testKey)
	_, err := security.DecryptGCM(legacy, testKey)
	s.T.Expect(err).Not().ToBeNil()
}

// TestDecryptAny_HandlesBothFormats is U-S1's "old ciphertext still
// decrypts" requirement made concrete: a caller reading a column that may
// hold either format shouldn't need to know which one a given row used.
func (s *SecuritySuite) TestDecryptAny_HandlesBothFormats() {
	legacyCt, err := security.Encrypt("old value", testKey)
	s.T.Expect(err).ToBeNil()
	legacyPt, err := security.DecryptAny(legacyCt, testKey)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(legacyPt).ToEqual("old value")

	newCt, err := security.EncryptGCM("new value", testKey)
	s.T.Expect(err).ToBeNil()
	newPt, err := security.DecryptAny(newCt, testKey)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(newPt).ToEqual("new value")
}

// TestReEncryptToGCM_MigrationHelper: PLAN M1-01's "migration helper" —
// upgrades a legacy CBC value to the versioned GCM envelope, and is a no-op
// (safe to call unconditionally on every row) on a value already migrated.
func (s *SecuritySuite) TestReEncryptToGCM_MigrationHelper() {
	legacyCt, err := security.Encrypt("migrate me", testKey)
	s.T.Expect(err).ToBeNil()

	migrated, err := security.ReEncryptToGCM(legacyCt, testKey)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(security.IsGCMEnvelope(migrated)).ToEqual(true)

	pt, err := security.DecryptGCM(migrated, testKey)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(pt).ToEqual("migrate me")

	// Idempotent: re-running on an already-migrated value returns it as-is.
	again, err := security.ReEncryptToGCM(migrated, testKey)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(again).ToEqual(migrated)
}

func (s *SecuritySuite) TestReEncryptToGCM_EmptyString() {
	migrated, err := security.ReEncryptToGCM("", testKey)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(migrated).ToEqual("")
}

// tamperHex flips one hex nibble roughly in the middle of s, corrupting the
// underlying byte without producing invalid hex.
func tamperHex(s string) string {
	i := len(s) / 2
	b := []byte(s)
	if b[i] == 'f' {
		b[i] = 'e'
	} else {
		b[i] = 'f'
	}
	return string(b)
}

func (s *SecuritySuite) TestHmac_Verify() {
	payload := map[string]any{"amount": 100, "ref": "abc"}
	sig, err := security.GenerateHmac(payload, "sha256", "secret-key")
	s.T.Expect(err).ToBeNil()
	s.T.Expect(security.CompareHmac(sig, payload, "sha256", "secret-key")).ToEqual(true)
	s.T.Expect(security.CompareHmac(sig, payload, "sha256", "wrong-key")).ToEqual(false)
}

func (s *SecuritySuite) TestHmac_SortedKeys() {
	// Order of keys in the map should not affect the signature
	a, _ := security.GenerateHmac(map[string]any{"b": 2, "a": 1}, "sha256", "k")
	b, _ := security.GenerateHmac(map[string]any{"a": 1, "b": 2}, "sha256", "k")
	s.T.Expect(a).ToEqual(b)
}

func (s *SecuritySuite) TestCheckSum() {
	h1 := security.CheckSum("hello")
	h2 := security.CheckSum("hello")
	h3 := security.CheckSum("world")
	s.T.Expect(h1).ToEqual(h2)
	s.T.Expect(h1).Not().ToEqual(h3)
	s.T.Expect(len(h1)).ToEqual(64) // sha256 hex = 64 chars
}

func (s *SecuritySuite) TestMD5() {
	h := security.MD5("hostname")
	s.T.Expect(len(h)).ToEqual(32)
}

// TestHmac_TopLevelOnlySort asserts that only the TOP-LEVEL keys are sorted —
// nested objects keep their original byte order. This mirrors TS sortObject(),
// which is a non-recursive top-level sort. The Go-side caller must use
// json.RawMessage for nested objects to retain JS-equivalent insertion order
// (plain map[string]any loses order because Go maps are unordered).
// Without the top-level-only behavior, gox sorts nested keys too and the
// resulting HMAC stops matching what a TS service produces for the same
// payload — breaking cross-language webhook signing.
func (s *SecuritySuite) TestHmac_TopLevelOnlySort() {
	// Two payloads with the same top-level keys but DIFFERENT nested key order.
	// json.RawMessage preserves the byte order verbatim; the HMAC therefore
	// reflects that order — they must NOT collapse to the same signature.
	a := map[string]any{
		"meta": json.RawMessage(`{"b":2,"a":1}`),
		"top":  "v",
	}
	b := map[string]any{
		"meta": json.RawMessage(`{"a":1,"b":2}`),
		"top":  "v",
	}
	sigA, _ := security.GenerateHmac(a, "sha256", "k")
	sigB, _ := security.GenerateHmac(b, "sha256", "k")
	s.T.Expect(sigA).Not().ToEqual(sigB)

	// Top-level reorder must yield the SAME signature when the nested
	// bytes are identical — proving top-level keys ARE sorted before signing.
	c := map[string]any{
		"top":  "v",
		"meta": json.RawMessage(`{"a":1,"b":2}`),
	}
	sigC, _ := security.GenerateHmac(c, "sha256", "k")
	s.T.Expect(sigB).ToEqual(sigC)
}

func (s *SecuritySuite) TestHmac_NonObjectPayloadStillWorks() {
	// sortedJSON falls through to plain marshalling for non-objects.
	sigStr, errStr := security.GenerateHmac("hello", "sha256", "k")
	s.T.Expect(errStr).ToBeNil()
	s.T.Expect(len(sigStr)).ToEqual(64)

	sigNum, errNum := security.GenerateHmac(42, "sha256", "k")
	s.T.Expect(errNum).ToBeNil()
	s.T.Expect(len(sigNum)).ToEqual(64)
}

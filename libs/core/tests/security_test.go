package tests

import (
	"testing"

	"github.com/thescaffold/gox-packages-core/security"
	test "github.com/awesome-goose/goose/testing"
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

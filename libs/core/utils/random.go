package utils

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"math/rand/v2"
	"strings"
)

const (
	alphaChars    = "abcdefghijklmnopqrstuvwxyz"
	numChars      = "0123456789"
	alphaNumChars = alphaChars + numChars
)

// RandomDigits returns a random numeric string of length n.
func RandomDigits(n int) string {
	return randomFrom(numChars, n)
}

// Random returns a random hex string of length n. Mirrors TS common.util.ts
// random(): randomBytes(n).toString('hex').substring(0, n) — n bytes of crypto
// randomness, hex-encoded, then sliced to n hex characters.
func Random(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	if _, err := cryptorand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(rand.IntN(256))
		}
	}
	return hex.EncodeToString(b)[:n]
}

// RandomUsername returns a random username like "A-123-456-789".
func RandomUsername() string {
	return strings.ToUpper(
		randomFrom(alphaChars, 1) + "-" +
			randomFrom(numChars, 3) + "-" +
			randomFrom(numChars, 3) + "-" +
			randomFrom(numChars, 3),
	)
}

// RandomReferralCode returns a code like "O-123-ABC".
func RandomReferralCode(initial string) string {
	if initial == "" {
		initial = "O"
	}
	return strings.ToUpper(
		initial + "-" + randomFrom(numChars, 3) + "-" + randomFrom(alphaChars, 3),
	)
}

func randomFrom(charset string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.IntN(len(charset))]
	}
	return string(b)
}

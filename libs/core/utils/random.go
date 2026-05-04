package utils

import (
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

// Random returns a random alphanumeric string of length n.
func Random(n int) string {
	return randomFrom(alphaNumChars, n)
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

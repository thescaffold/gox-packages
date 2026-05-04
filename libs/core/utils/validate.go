package utils

import (
	"regexp"
	"strings"
)

var (
	emailRe = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	// Nigerian: +2340XXXXXXXXX (14), 2340XXXXXXXXX (13), 0XXXXXXXXXX (11)
	phoneRe = regexp.MustCompile(`^(\+234|234|0)[789][01]\d{8}$`)
	numRe   = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
)

// IsEmail returns true if v is a valid email address.
func IsEmail(v string) bool {
	return emailRe.MatchString(strings.TrimSpace(v))
}

// IsPhone returns true if v is a valid Nigerian phone number.
func IsPhone(v string) bool {
	return phoneRe.MatchString(strings.TrimSpace(v))
}

// IsNumeric returns true if v is a numeric string (integer or decimal).
func IsNumeric(v string) bool {
	if v == "" {
		return false
	}
	return numRe.MatchString(strings.TrimSpace(v))
}

// IsNullOrUndefined returns true if v is nil.
// In Go there is no "undefined"; nil covers both TS null and undefined.
func IsNullOrUndefined(v any) bool {
	return v == nil
}

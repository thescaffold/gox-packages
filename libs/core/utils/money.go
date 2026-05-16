package utils

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// ToMajor converts a minor-unit integer to a fixed 2-decimal string.
// Mirrors TS money.util.ts toMajor(): there is no `decimals` parameter — the
// value is always rendered with exactly 2 fractional digits. The TS impl
// pads the digit string to at least 3 chars then slices the last 2 off as the
// fraction, so e.g. 5 → "0.05", 50 → "0.50", 1050 → "10.50".
// Negative input is rejected, matching the TS `minor >= 0` guard.
func ToMajor(minor int64) (string, error) {
	if minor < 0 {
		return "", errors.New("invalid number input")
	}
	str := strconv.FormatInt(minor, 10)
	if len(str) < 3 {
		str = strings.Repeat("0", 3-len(str)) + str
	}
	whole := str[:len(str)-2]
	fraction := str[len(str)-2:]
	return whole + "." + fraction, nil
}

// ToMinor converts a decimal string to a minor-unit integer with a fixed 2
// decimal places. Mirrors TS money.util.ts toMinor(): the fractional part is
// right-padded with "00" and truncated to 2 digits, then concatenated to the
// whole part. e.g. "10.5" → 1050, "10" → 1000, "10.999" → 1099.
// The result must be a non-negative integer.
func ToMinor(amount string) (int64, error) {
	whole, fraction, _ := strings.Cut(amount, ".")
	truncatedFraction := (fraction + "00")[:2]
	combined := whole + truncatedFraction

	n, err := strconv.ParseInt(combined, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("invalid or unsafe positive amount")
	}
	return n, nil
}

// ToInt truncates a decimal amount to its integer part. Mirrors TS
// money.util.ts toInt(): the amount is parsed as a float, must be finite and
// non-negative, then truncated toward zero.
func ToInt(amount string) (int64, error) {
	num, err := strconv.ParseFloat(strings.TrimSpace(amount), 64)
	if err != nil || math.IsInf(num, 0) || math.IsNaN(num) || num < 0 {
		return 0, errors.New("amount must be a finite, non-negative number")
	}
	return int64(math.Trunc(num)), nil
}

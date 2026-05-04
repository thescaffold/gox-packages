package utils

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ToMajor converts a minor-unit integer to a decimal string.
// e.g. ToMajor(1050, 2) → "10.50"
func ToMajor(minor int64, decimals int) string {
	if decimals <= 0 {
		decimals = 2
	}
	divisor := int64(math.Pow10(decimals))
	whole := minor / divisor
	fraction := minor % divisor
	if fraction < 0 {
		fraction = -fraction
	}
	return fmt.Sprintf("%d.%0*d", whole, decimals, fraction)
}

// ToMinor converts a decimal string to a minor-unit integer.
// e.g. ToMinor("10.50", 2) → 1050
func ToMinor(amount string, decimals int) (int64, error) {
	if decimals <= 0 {
		decimals = 2
	}
	parts := strings.SplitN(amount, ".", 2)
	whole := parts[0]
	var fraction string
	if len(parts) == 2 {
		fraction = parts[1]
	}

	// Pad or truncate fraction to `decimals` digits
	if len(fraction) < decimals {
		fraction = fraction + strings.Repeat("0", decimals-len(fraction))
	} else {
		fraction = fraction[:decimals]
	}

	combined := whole + fraction
	n, err := strconv.ParseInt(combined, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %s", amount)
	}
	if n < 0 {
		return 0, fmt.Errorf("amount must be non-negative")
	}
	return n, nil
}

// ToInt truncates a decimal amount to its integer part, validating it is non-negative.
func ToInt(amount string) (int64, error) {
	parts := strings.SplitN(amount, ".", 2)
	n, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %s", amount)
	}
	if n < 0 {
		return 0, fmt.Errorf("amount must be non-negative")
	}
	return n, nil
}

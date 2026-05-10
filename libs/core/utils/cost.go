package utils

import (
	"math"
	"time"
)

// CurrencyMeta mirrors TS defaultCurrencyMeta in cost.util.ts.
type CurrencyMeta struct {
	Rate                        string
	Symbol                      string
	SymbolOnLeft                bool
	DecimalDigits               int
	DecimalSeparator            string
	ThousandsSeparator          string
	SpaceBetweenAmountAndSymbol bool
}

// DefaultCurrencyMeta is the package-level default currency configuration.
// Mirrors TS defaultCurrencyMeta exactly.
var DefaultCurrencyMeta = CurrencyMeta{
	Rate:                        "1",
	Symbol:                      "$",
	SymbolOnLeft:                true,
	DecimalDigits:               2,
	DecimalSeparator:            ".",
	ThousandsSeparator:          ",",
	SpaceBetweenAmountAndSymbol: false,
}

// CalculateProratedCost returns the prorated cost for the remaining days of
// the current month, with a floor of 100 minor units. Mirrors TS
// calculateProratedCost(totalCost) in cost.util.ts.
func CalculateProratedCost(totalCost float64) int64 {
	now := time.Now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	elapsed := now.Sub(startOfMonth)
	elapsedDays := int(elapsed.Hours() / 24)
	daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, now.Location()).Day()
	remainingDays := daysInMonth - elapsedDays

	prorated := float64(remainingDays) / float64(daysInMonth) * totalCost
	if prorated < 100 {
		prorated = 100
	}
	return int64(math.Floor(prorated))
}

// CalculateAdaptiveCost mirrors TS calculateAdaptiveCost: round base to int,
// nil/zero treated as 0. The rate parameter is currently unused (matching TS).
func CalculateAdaptiveCost(base float64, _ float64) int64 {
	return int64(math.Floor(base))
}

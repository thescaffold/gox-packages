package utils

import "time"

// Time format constants matching the TS FORMAT object.
const (
	FormatDate         = "02-01"
	FormatFullDate     = "2006-01-02"
	FormatDateTime     = "2006-01-02 03:04"
	FormatFullDateTime = "2006-01-02 03:04:05"
	FormatTime         = "03:04"
	FormatFullTime     = "03:04:05"
	FormatPretty       = "Mon, 02 Jan 2006, 03:04 PM"
)

// Now returns the current UTC time.
func Now() time.Time {
	return time.Now().UTC()
}

// Format formats t using one of the Format* constants (or any Go layout string).
func Format(t time.Time, layout string) string {
	return t.Format(layout)
}

// ── time.utc() / time.tz() namespaces ────────────────────────────────────────
//
// Mirror TS time.utils.ts which exposes time.utc() and time.tz(timezone) with
// methods now(), isValid(), from(), compareTime(), isEqual(), fromTz/fromUtc().
//
// Usage:
//
//	utils.UTC().Now()
//	utils.TZ("America/New_York").From("2024-01-01")
//	utils.UTC().IsEqual(a, b)

// UtcOps is the time.utc() namespace.
type UtcOps struct{}

// TzOps is the time.tz(timezone) namespace.
type TzOps struct{ loc *time.Location }

// UTC returns the time.utc() namespace.
func UTC() UtcOps { return UtcOps{} }

// TZ returns the time.tz(timezone) namespace. An empty string defaults to
// America/New_York (the TS default). Invalid zones fall back to UTC.
func TZ(timezone string) TzOps {
	if timezone == "" {
		timezone = "America/New_York"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	return TzOps{loc: loc}
}

// ── UTC namespace methods ────────────────────────────────────────────────────

// Now returns the current time in UTC.
func (UtcOps) Now() time.Time { return time.Now().UTC() }

// From parses date and returns it in UTC. Accepts time.Time, string (RFC3339,
// FullDate, FullDateTime), or int64/float64 epoch milliseconds.
func (UtcOps) From(date any) time.Time { return parseToUTC(date) }

// IsValid reports whether date can be parsed.
func (UtcOps) IsValid(date any) bool { return !parseToUTC(date).IsZero() }

// CompareTime returns the difference (first - second) in milliseconds.
func (UtcOps) CompareTime(first, second any) int64 {
	return parseToUTC(first).Sub(parseToUTC(second)).Milliseconds()
}

// IsEqual returns true when first and second fall on the same calendar day in UTC.
func (UtcOps) IsEqual(first, second any) bool {
	a, b := parseToUTC(first), parseToUTC(second)
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

// FromTz interprets date as wall-clock time in the given timezone, then converts to UTC.
func (UtcOps) FromTz(date any, timezone string) time.Time {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return parseToUTC(date)
	}
	return parseInLocation(date, loc).UTC()
}

// ── TZ namespace methods ─────────────────────────────────────────────────────

// Now returns the current time in this TZ.
func (t TzOps) Now() time.Time { return time.Now().In(t.loc) }

// From parses date and converts to this TZ.
func (t TzOps) From(date any) time.Time { return parseToUTC(date).In(t.loc) }

// IsValid reports whether date can be parsed.
func (t TzOps) IsValid(date any) bool { return !parseToUTC(date).IsZero() }

// CompareTime returns first - second in milliseconds (after both are coerced to this TZ).
func (t TzOps) CompareTime(first, second any) int64 {
	return parseToUTC(first).Sub(parseToUTC(second)).Milliseconds()
}

// IsEqual returns true when first and second fall on the same calendar day in this TZ.
func (t TzOps) IsEqual(first, second any) bool {
	a := parseToUTC(first).In(t.loc)
	b := parseToUTC(second).In(t.loc)
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

// FromUtc interprets date as UTC and converts to this TZ.
func (t TzOps) FromUtc(date any) time.Time { return parseToUTC(date).In(t.loc) }

// StrToDate parses a UTC date string, returning a time.Time. For backward compat
// with the TS strToDate helper.
func StrToDate(value string) time.Time { return parseToUTC(value) }

// ── parsing helpers ──────────────────────────────────────────────────────────

// parseToUTC accepts time.Time / string / numeric epoch and returns UTC.
// Returns the zero time on parse failure.
func parseToUTC(v any) time.Time {
	switch x := v.(type) {
	case time.Time:
		return x.UTC()
	case *time.Time:
		if x == nil {
			return time.Time{}
		}
		return x.UTC()
	case int64:
		return time.UnixMilli(x).UTC()
	case int:
		return time.UnixMilli(int64(x)).UTC()
	case float64:
		return time.UnixMilli(int64(x)).UTC()
	case string:
		return parseString(x).UTC()
	}
	return time.Time{}
}

func parseInLocation(v any, loc *time.Location) time.Time {
	if s, ok := v.(string); ok {
		for _, layout := range parseLayouts {
			if t, err := time.ParseInLocation(layout, s, loc); err == nil {
				return t
			}
		}
	}
	t := parseToUTC(v)
	if t.IsZero() {
		return t
	}
	// rebind to the requested location keeping wall clock
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
}

var parseLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"02-01-2006",
}

func parseString(s string) time.Time {
	for _, layout := range parseLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

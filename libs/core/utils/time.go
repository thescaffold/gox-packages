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

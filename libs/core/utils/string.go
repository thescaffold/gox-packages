package utils

import (
	"strings"
	"unicode"
)

// UCFirst uppercases the first character of the string.
func UCFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// TitleCase capitalizes the first letter of each space-separated word.
func TitleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = UCFirst(w)
	}
	return strings.Join(words, " ")
}

// Prettify replaces underscores with spaces.
func Prettify(s string) string {
	return strings.ReplaceAll(s, "_", " ")
}

// TrimString removes leading/trailing slashes (and replaces them with replaceWith).
func TrimString(s, replaceWith string) string {
	trimmed := strings.Trim(s, "/")
	if replaceWith == "" {
		return trimmed
	}
	return strings.ReplaceAll(trimmed, "/", replaceWith)
}

// MaskEmail masks an email address, showing first 3 and last 2 chars of the username.
// "john.doe@example.com" → "joh****oe@example.com"
func MaskEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return email
	}
	user := parts[0]
	domain := parts[1]

	if len(user) <= 5 {
		return email
	}

	first := user[:3]
	last := user[len(user)-2:]
	stars := strings.Repeat("*", len(user)-5)
	return first + stars + last + "@" + domain
}

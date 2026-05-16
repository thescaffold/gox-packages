package utils

import (
	"strings"
	"unicode"
)

// UCFirst uppercases the first character of the string.
// Mirrors TS common.util.ts ucfirst().
func UCFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// TitleCase capitalizes the first letter of each word and lowercases the rest.
// Mirrors TS common.util.ts titleCase() exactly: it walks the string char by
// char — a space resets to "upper", every other character is upper-cased when
// it starts a word and lower-cased otherwise. Multiple/leading spaces are
// preserved (unlike a Fields-based split).
func TitleCase(s string) string {
	if s == "" {
		return s
	}
	upper := true
	var b strings.Builder
	for _, r := range s {
		if r == ' ' {
			upper = true
			b.WriteRune(' ')
			continue
		}
		if upper {
			b.WriteRune(unicode.ToUpper(r))
		} else {
			b.WriteRune(unicode.ToLower(r))
		}
		upper = false
	}
	return b.String()
}

// Prettify replaces underscores with spaces. Mirrors TS prettify().
func Prettify(s string) string {
	return strings.ReplaceAll(s, "_", " ")
}

// TrimString replaces a single leading slash and a single trailing slash with
// replaceWith. Mirrors TS common.util.ts trimString(): it is implemented as
// `s.replace(/^\//, replaceWith).replace(/\/$/, replaceWith)` — only ONE slash
// is removed from each end, and inner slashes are left untouched.
func TrimString(s, replaceWith string) string {
	if strings.HasPrefix(s, "/") {
		s = replaceWith + s[1:]
	}
	if strings.HasSuffix(s, "/") {
		s = s[:len(s)-1] + replaceWith
	}
	return s
}

// PathParts is the 5-tuple returned by ExtractPath, mirroring the TS
// extractPath() return array [group, service, resource, resourceId, component].
// An absent component is the empty string (the Go analogue of TS null).
type PathParts struct {
	Group      string
	Service    string
	Resource   string
	ResourceID string
	Component  string
}

// ExtractPath splits a URL path into its NTX route components, mirroring TS
// common.util.ts extractPath(): the query string is dropped, then TrimString
// removes a single leading and trailing slash, then the remainder is split on
// "/" into [group, service, resource, ...slugs]. Exactly one trailing slug is
// treated as the component; exactly two slugs are [resourceId, component]; any
// other slug count leaves both resourceId and component empty.
func ExtractPath(url string) PathParts {
	if i := strings.IndexByte(url, '?'); i >= 0 {
		url = url[:i]
	}
	segs := strings.Split(TrimString(url, ""), "/")
	var p PathParts
	if len(segs) > 0 {
		p.Group = segs[0]
	}
	if len(segs) > 1 {
		p.Service = segs[1]
	}
	if len(segs) > 2 {
		p.Resource = segs[2]
	}
	var slugs []string
	if len(segs) > 3 {
		slugs = segs[3:]
	}
	switch len(slugs) {
	case 1:
		p.Component = slugs[0]
	case 2:
		p.ResourceID = slugs[0]
		p.Component = slugs[1]
	}
	return p
}

// Mask returns a fixed 7-character mask for any non-empty value, mirroring TS
// common.util.ts mask(): `if (!val) return null; return '*******'`. The `use`
// parameter in TS is accepted but unused, so it is omitted here. An empty input
// yields an empty string (the Go analogue of TS `null`).
func Mask(val string) string {
	if val == "" {
		return ""
	}
	return "*******"
}

// MaskEmail masks the username portion of an email address. Mirrors TS
// common.util.ts maskEmail(): the masked username is
// `username.slice(0,3) + '*'.repeat(max(0, len-2)) + username.slice(-2)`.
// Note the visible start (3) and end (2) can overlap the mask for short
// usernames — that is the TS behavior. TS throws on a missing username/domain;
// here the input is returned unchanged in that case.
func MaskEmail(email string) string {
	// TS does `[username, domain] = email.split('@')`, so for inputs with more
	// than one '@' only the first two segments are used.
	segments := strings.Split(email, "@")
	if len(segments) < 2 {
		return email
	}
	username, domain := segments[0], segments[1]
	if username == "" || domain == "" {
		return email
	}

	// slice(0, 3)
	start := username
	if len(username) > 3 {
		start = username[:3]
	}
	// slice(-2)
	end := username
	if len(username) >= 2 {
		end = username[len(username)-2:]
	}
	// '*'.repeat(max(0, len-2))
	stars := 0
	if len(username) > 2 {
		stars = len(username) - 2
	}
	return start + strings.Repeat("*", stars) + end + "@" + domain
}

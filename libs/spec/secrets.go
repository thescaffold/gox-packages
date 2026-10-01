package spec

import (
	"fmt"
	"strings"
	"unicode"
)

// ScanSecrets looks for text that looks like a password, key or token. Spec text
// flows into prompts and the repository, so a commit that contains one should be
// refused with a plain message. Lines are those of the text given.
func ScanSecrets(text string) []Diagnostic {
	var out []Diagnostic
	for i, line := range strings.Split(text, "\n") {
		if what, col := secretIn(line); what != "" {
			out = append(out, Diagnostic{Line: i + 1, Col: col, Severity: Warning, Code: "secret",
				Message: fmt.Sprintf("This looks like a password or key (“%s…”). Put it in the environment's settings instead of the spec.", what)})
		}
	}
	return out
}

// HasSecret reports whether any diagnostic is a secret finding.
func HasSecret(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Code == "secret" {
			return true
		}
	}
	return false
}

var secretPrefixes = []struct {
	prefix string
	min    int
}{
	{"sk-", 20}, {"sk_live_", 20}, {"sk_test_", 20}, {"rk_live_", 20},
	{"ghp_", 30}, {"gho_", 30}, {"ghu_", 30}, {"ghs_", 30}, {"ghr_", 30}, {"github_pat_", 30},
	{"xoxb-", 20}, {"xoxp-", 20}, {"xoxa-", 20}, {"xoxs-", 20}, {"glpat-", 20}, {"npm_", 36}, {"AIza", 39},
}

func tokenChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_-./+=", r)
}

// secretIn reports the start of the first secret-looking thing on a line.
func secretIn(line string) (what string, col int) {
	if strings.Contains(line, "-----BEGIN") && strings.Contains(line, "PRIVATE KEY") {
		return "-----BEGIN", strings.Index(line, "-----BEGIN") + 1
	}
	start := -1
	flush := func(end int) (string, int) {
		if start < 0 {
			return "", 0
		}
		tok := line[start:end]
		at := start + 1
		start = -1
		if looksLikeKey(tok) {
			return tok[:min(4, len(tok))], at
		}
		return "", 0
	}
	for i, r := range line {
		if tokenChar(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if w, c := flush(i); w != "" {
			return w, c
		}
	}
	if w, c := flush(len(line)); w != "" {
		return w, c
	}
	return assignedSecret(line)
}

func looksLikeKey(tok string) bool {
	for _, p := range secretPrefixes {
		if strings.HasPrefix(tok, p.prefix) && len(tok) >= p.min {
			return true
		}
	}
	if (strings.HasPrefix(tok, "AKIA") || strings.HasPrefix(tok, "ASIA")) && len(tok) == 20 && upperAlnum(tok) {
		return true
	}
	if strings.HasPrefix(tok, "eyJ") && strings.Count(tok, ".") == 2 && len(tok) >= 30 {
		return true
	}
	return false
}

func upperAlnum(s string) bool {
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

var secretNames = []string{"password", "passwd", "secret", "token", "apikey", "api key", "api_key", "api-key", "private key"}

// assignedSecret finds "password: hunter2abc" and "token = ..." where the value
// is not the name of a setting (PAYSTACK_SECRET) or an ordinary word.
func assignedSecret(line string) (string, int) {
	sep := strings.IndexAny(line, ":=")
	if sep < 0 {
		return "", 0
	}
	name := strings.ToLower(strings.Trim(line[:sep], " \t-*#>\"'`"))
	hit := false
	for _, n := range secretNames {
		if strings.HasSuffix(name, n) {
			hit = true
		}
	}
	if !hit {
		return "", 0
	}
	rest := strings.TrimSpace(line[sep+1:])
	val := strings.Trim(rest, "\"'`.,;")
	if val == "" || strings.ContainsAny(val, " \t[]") || len(val) < 8 {
		return "", 0
	}
	if upperIdent(val) {
		return "", 0
	}
	var letters, digits int
	for _, r := range val {
		switch {
		case unicode.IsLetter(r):
			letters++
		case unicode.IsDigit(r):
			digits++
		}
	}
	if len(val) >= 20 || (letters > 0 && digits > 0) {
		return val[:min(4, len(val))], strings.Index(line, rest) + 1
	}
	return "", 0
}

// upperIdent is an environment key such as PAYSTACK_SECRET: it names a secret, it is not one.
func upperIdent(s string) bool {
	if s == "" || !(s[0] >= 'A' && s[0] <= 'Z') {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

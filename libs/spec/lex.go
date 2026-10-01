package spec

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type lineKind int

const (
	kBlank    lineKind = iota
	kTitle             // "# text"
	kSection           // "## text"
	kBlock             // "### text"
	kDeep              // "#### text" and deeper
	kQuote             // "> text"
	kQuestion          // "? text"
	kBullet            // "- text", "* text", "+ text"
	kProp              // "key: value"
	kComment           // "<!--"
	kFence             // "```" or "~~~"
	kText
)

// tok is one classified line. Classification looks at the line alone, which is
// what makes printing and re-reading agree.
type tok struct {
	n      int
	raw    string // the line without its terminator
	kind   lineKind
	indent int
	// text is the heading text, the bullet text after its marker, the question
	// text or the property value, trimmed.
	text string
	key  string // property key
}

func lex(src string) []tok {
	if src == "" {
		return nil
	}
	parts := strings.Split(src, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	toks := make([]tok, len(parts))
	for i, l := range parts {
		// trailing space and carriage returns are not content: lines never keep them
		toks[i] = lexLine(i+1, strings.TrimRight(l, " \t\r"))
	}
	return toks
}

func lexLine(n int, raw string) tok {
	t := tok{n: n, raw: raw}
	indent, at := 0, 0
	for at < len(raw) && (raw[at] == ' ' || raw[at] == '\t') {
		if raw[at] == '\t' {
			indent += 4
		} else {
			indent++
		}
		at++
	}
	t.indent = indent
	c := raw[at:]
	if strings.TrimSpace(c) == "" {
		t.kind = kBlank
		return t
	}
	t.kind = kText
	if indent == 0 && c[0] == '#' {
		h := 0
		for h < len(c) && c[h] == '#' {
			h++
		}
		if h <= 6 && h < len(c) && (c[h] == ' ' || c[h] == '\t') {
			if txt := strings.TrimSpace(c[h+1:]); txt != "" {
				t.text = txt
				switch h {
				case 1:
					t.kind = kTitle
				case 2:
					t.kind = kSection
				case 3:
					t.kind = kBlock
				default:
					t.kind = kDeep
				}
				return t
			}
		}
		return t
	}
	if indent == 0 && c[0] == '>' {
		t.kind = kQuote
		t.text = strings.TrimSpace(c[1:])
		return t
	}
	if indent == 0 && strings.HasPrefix(c, "<!--") {
		t.kind = kComment
		return t
	}
	if indent == 0 && (strings.HasPrefix(c, "```") || strings.HasPrefix(c, "~~~")) {
		t.kind = kFence
		return t
	}
	if indent == 0 && strings.HasPrefix(c, "? ") {
		if txt := strings.TrimSpace(c[2:]); txt != "" {
			t.kind = kQuestion
			t.text = txt
			return t
		}
	}
	if len(c) > 1 && (c[0] == '-' || c[0] == '*' || c[0] == '+') && (c[1] == ' ' || c[1] == '\t') {
		if txt := strings.TrimSpace(c[2:]); txt != "" {
			t.kind = kBullet
			t.text = txt
			return t
		}
	}
	if indent == 0 {
		if k, v, ok := propKey(c); ok {
			t.kind = kProp
			t.key = k
			t.text = v
			return t
		}
	}
	return t
}

// propKey reads "key: value" where the key is one to three lowercase words.
func propKey(s string) (key, val string, ok bool) {
	i, words := 0, 0
	for {
		j := i
		for j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] >= '0' && s[j] <= '9') {
			j++
		}
		if j == i || (words == 0 && s[i] >= '0' && s[i] <= '9') {
			return "", "", false
		}
		words++
		i = j
		if i < len(s) && s[i] == ' ' && words < 3 {
			i++
			continue
		}
		break
	}
	if i >= len(s) || s[i] != ':' {
		return "", "", false
	}
	if i+1 < len(s) && s[i+1] != ' ' && s[i+1] != '\t' {
		return "", "", false
	}
	return s[:i], strings.TrimSpace(s[i+1:]), true
}

func validID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') {
			return false
		}
	}
	return utf8.ValidString(s)
}

// idProblem describes an id marker at the end of a line that is not usable.
type idProblem struct {
	code string
	col  int // 1-based byte column of the "{#"
	end  int // 1-based byte column just after the marker
}

// splitTrailingID takes a trailing " {#id}" off s. A marker that is there but
// unusable stays in the text and is reported.
func splitTrailingID(s string) (rest, id string, prob *idProblem) {
	s = strings.TrimSpace(s)
	idx := strings.LastIndex(s, "{#")
	if idx < 0 {
		return s, "", nil
	}
	tail := s[idx+2:]
	if strings.ContainsAny(tail, " \t") {
		return s, "", nil
	}
	if strings.HasSuffix(tail, "}") {
		inner := tail[:len(tail)-1]
		if strings.Contains(inner, "}") || strings.Contains(inner, "{") {
			return s, "", nil
		}
		if validID(inner) && (idx == 0 || s[idx-1] == ' ' || s[idx-1] == '\t') {
			return strings.TrimSpace(s[:idx]), inner, nil
		}
		return s, "", &idProblem{code: "id-invalid", col: idx + 1, end: len(s) + 1}
	}
	if strings.ContainsAny(tail, "{}") {
		return s, "", nil
	}
	return s, "", &idProblem{code: "id-unclosed", col: idx + 1, end: len(s) + 1}
}

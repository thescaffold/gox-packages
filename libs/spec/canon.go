package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// Fix records one change Canonicalize made, so a person can be told.
type Fix struct {
	Line    int
	Message string
}

// Canonicalize makes a document ready to commit: it sets the version, gives
// every item (and every rule and criterion of a feature) a stable id, re-ids the
// copy when an id appears twice, and, when base is the previous revision, keeps
// references right where an item was renamed: "[[Old title]]" becomes
// "[[New title]]" because the item's id is the same in both.
func Canonicalize(d *Doc, base *Doc) []Fix {
	var fixes []Fix
	if d.Version == "" {
		d.Version = "1"
	}
	ids := newIDSet(d)
	fixes = append(fixes, ids.dedupe(d)...)
	fixes = append(fixes, ids.assign(d)...)
	if base != nil {
		fixes = append(fixes, rewriteRenamed(d, base)...)
	}
	return fixes
}

type idSet struct {
	used map[string]bool
	// next remembers, per base, the suffix last handed out. Ids are only ever
	// added, so everything below it is still taken and the search can resume
	// there: a thousand copies of one title cost a thousand steps, not half a million.
	next map[string]int
}

func newIDSet(*Doc) *idSet { return &idSet{used: map[string]bool{}, next: map[string]int{}} }

// eachIDSlot visits every place that can hold an id, in document order, with
// the id that place would like to have.
func eachIDSlot(d *Doc, fn func(id *string, want func() string, line int)) {
	for _, s := range d.Sections {
		for _, n := range s.Nodes {
			it, ok := n.(*Item)
			if !ok {
				continue
			}
			fn(&it.ID, func() string { return itemWant(it) }, it.Line)
			for _, b := range it.Body {
				switch b := b.(type) {
				case *ListProp:
					_, identified := identifiedLists[b.Key]
					for _, e := range b.Entries {
						if identified || e.ID != "" {
							fn(&e.ID, func() string { return anon("entry:"+b.Key+":"+it.ID, e.Text) }, e.Line)
						}
					}
				case *Child:
					if b.ID != "" {
						fn(&b.ID, func() string { return anon("child:"+it.ID, b.Text) }, b.Line)
					}
				}
			}
		}
	}
}

// itemWant is the id an item would like: a question's, decision's or
// assumption's first words with a letter prefix, a titled item's title, or an
// anonymous c- id.
func itemWant(it *Item) string {
	switch {
	case it.Style == StyleQuestion:
		return "q-" + slug(it.Text, 5, "question")
	case it.Title != "":
		return slug(it.Title, 8, "item")
	case it.Kind == "decision":
		return "d-" + slug(it.Text, 5, "decision")
	case it.Kind == "assumption":
		return "a-" + slug(it.Text, 5, "assumption")
	}
	return anon("item:"+it.Kind, it.Text)
}

// anon is c- and four hex digits of a hash of what the thing says.
func anon(kind, text string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + text))
	return "c-" + hex.EncodeToString(sum[:])[:4]
}

// dedupe gives a repeated id's later copies a new id. Every id already in the
// document is reserved first, so a new one never takes an id used further down.
func (s *idSet) dedupe(d *Doc) []Fix {
	eachIDSlot(d, func(id *string, _ func() string, _ int) {
		if *id != "" {
			s.used[*id] = true
		}
	})
	var fixes []Fix
	first := map[string]bool{}
	eachIDSlot(d, func(id *string, want func() string, line int) {
		if *id == "" {
			return
		}
		if first[*id] {
			old := *id
			*id = s.fresh(want())
			fixes = append(fixes, Fix{Line: line, Message: "The id “" + old + "” was used twice, so this copy was given the new id “" + *id + "”."})
			return
		}
		first[*id] = true
	})
	return fixes
}

func (s *idSet) assign(d *Doc) []Fix {
	var fixes []Fix
	eachIDSlot(d, func(id *string, want func() string, line int) {
		if *id != "" {
			return
		}
		*id = s.fresh(want())
		fixes = append(fixes, Fix{Line: line, Message: "Gave this item the id “" + *id + "”."})
	})
	return fixes
}

// fresh returns base, or base with -2, -3 ... until it is unused, and marks it used.
func (s *idSet) fresh(base string) string {
	id := base
	n := 2
	if s.used[id] {
		if h := s.next[base]; h > n {
			n = h
		}
		for id = base + "-" + itoa(n); s.used[id]; id = base + "-" + itoa(n) {
			n++
		}
		s.next[base] = n
	}
	s.used[id] = true
	return id
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// slug lowercases text, keeps letters and digits, joins words with "-" and keeps
// at most maxWords words.
func slug(s string, maxWords int, fallback string) string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	if len(words) > maxWords {
		words = words[:maxWords]
	}
	out := strings.Join(words, "-")
	if len(out) > 48 {
		out = out[:48]
		for len(out) > 0 && !validTail(out) {
			out = out[:len(out)-1]
		}
		out = strings.TrimRight(out, "-")
	}
	if out == "" {
		return fallback
	}
	return out
}

// validTail reports whether s is valid UTF-8 (a byte cut can split a rune).
func validTail(s string) bool {
	for _, r := range s {
		if r == unicode.ReplacementChar {
			return false
		}
	}
	return true
}

// rewriteRenamed finds items whose id exists in base under a different title and
// rewrites "[[Old title]]" to "[[New title]]" throughout d.
func rewriteRenamed(d, base *Doc) []Fix {
	oldTitle := map[string]string{}
	for _, s := range base.Sections {
		for _, n := range s.Nodes {
			if it, ok := n.(*Item); ok && it.ID != "" && it.Title != "" {
				oldTitle[it.ID] = it.Title
			}
		}
	}
	current := map[string]bool{}
	renames := map[string]string{}
	line := map[string]int{}
	for _, s := range d.Sections {
		for _, n := range s.Nodes {
			if it, ok := n.(*Item); ok && it.Title != "" {
				current[normTitle(it.Title)] = true
				if old, ok := oldTitle[it.ID]; ok && normTitle(old) != normTitle(it.Title) {
					renames[normTitle(old)] = it.Title
					line[normTitle(old)] = it.Line
				}
			}
		}
	}
	var fixes []Fix
	for old, nw := range renames {
		if current[old] { // the old title is in use by another item now: leave it alone
			delete(renames, old)
			continue
		}
		fixes = append(fixes, Fix{Line: line[old], Message: "“" + nw + "” was renamed, so references to its old name now use the new one."})
	}
	if len(renames) == 0 {
		return nil
	}
	d.eachText(func(s *string) {
		*s = rewriteRefs(*s, func(inner string) (string, bool) {
			if strings.HasPrefix(inner, "#") {
				return "", false
			}
			nw, ok := renames[normTitle(inner)]
			return nw, ok
		})
	})
	return fixes
}

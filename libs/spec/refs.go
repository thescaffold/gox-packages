package spec

import "strings"

// normTitle is how titles are compared when a reference is resolved:
// case-insensitive, spaces collapsed.
func normTitle(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// Ref is a "[[Title]]" or "[[#id]]" reference found in text.
type Ref struct {
	// Inner is the text between the brackets, trimmed.
	Inner string
	// ByID reports the "[[#id]]" form.
	ByID bool
}

// References lists the references in a piece of text, in order.
func References(s string) []Ref {
	var out []Ref
	rewriteRefs(s, func(inner string) (string, bool) {
		r := Ref{Inner: inner}
		if strings.HasPrefix(inner, "#") {
			r.Inner, r.ByID = strings.TrimSpace(inner[1:]), true
		}
		out = append(out, r)
		return "", false
	})
	return out
}

// rewriteRefs calls f with the trimmed inside of each "[[...]]"; when f reports
// true the reference is replaced by "[[" + result + "]]".
func rewriteRefs(s string, f func(inner string) (string, bool)) string {
	if !strings.Contains(s, "[[") {
		return s
	}
	var sb strings.Builder
	for {
		i := strings.Index(s, "[[")
		if i < 0 {
			break
		}
		j := strings.Index(s[i+2:], "]]")
		if j < 0 {
			break
		}
		inner := s[i+2 : i+2+j]
		if strings.ContainsAny(inner, "\n[") || strings.TrimSpace(inner) == "" {
			sb.WriteString(s[:i+2])
			s = s[i+2:]
			continue
		}
		sb.WriteString(s[:i])
		if repl, ok := f(strings.TrimSpace(inner)); ok {
			sb.WriteString("[[" + repl + "]]")
		} else {
			sb.WriteString(s[i : i+2+j+2])
		}
		s = s[i+2+j+2:]
	}
	sb.WriteString(s)
	return sb.String()
}

// eachText calls fn with a pointer to every piece of authored text that may hold
// references. Raw text and comments are left alone.
func (d *Doc) eachText(fn func(*string)) {
	d.eachTextAt(func(s *string, _ int) { fn(s) })
}

// eachTextAt is eachText with the line each piece was read from.
func (d *Doc) eachTextAt(fn func(*string, int)) {
	for i := range d.Settings {
		fn(&d.Settings[i].Value, d.Settings[i].Line)
	}
	for _, s := range d.Sections {
		for _, n := range s.Nodes {
			eachNodeText(n, fn)
		}
	}
}

func eachNodeText(n Node, fn func(*string, int)) {
	switch n := n.(type) {
	case *Para:
		for i := range n.Lines {
			fn(&n.Lines[i], n.Line+i)
		}
	case *Prop:
		fn(&n.Value, n.Line)
	case *ListProp:
		for _, e := range n.Entries {
			fn(&e.Text, e.Line)
		}
	case *Child:
		fn(&n.Text, n.Line)
		for i := range n.Cont {
			fn(&n.Cont[i], n.Line)
		}
	case *Item:
		fn(&n.Title, n.Line)
		fn(&n.Text, n.Line)
		for i := range n.Cont {
			fn(&n.Cont[i], n.Line)
		}
		for _, b := range n.Body {
			eachNodeText(b, fn)
		}
		for i := range n.Options {
			fn(&n.Options[i].Text, n.Options[i].Line)
		}
		fn(&n.Answer, n.Line)
	}
}

// Addr is something with an id: an item, or one rule or criterion of a feature.
type Addr struct {
	ID string
	// Kind is the item's kind, or "must", "criterion", "not_now" or "child".
	Kind string
	// Label is the title, name or text, for display.
	Label string
	// Parent is the id of the item it belongs to ("" for an item).
	Parent string
	Line   int
}

// Addressables lists everything that carries an id, in document order.
func (d *Doc) Addressables() []Addr {
	var out []Addr
	for _, s := range d.Sections {
		for _, n := range s.Nodes {
			it, ok := n.(*Item)
			if !ok {
				continue
			}
			if it.ID != "" {
				out = append(out, Addr{ID: it.ID, Kind: it.Kind, Label: itemLabel(it), Line: it.Line})
			}
			for _, b := range it.Body {
				switch b := b.(type) {
				case *ListProp:
					kind, ok := identifiedLists[b.Key]
					if !ok {
						kind = "child"
					}
					for _, e := range b.Entries {
						if e.ID != "" {
							out = append(out, Addr{ID: e.ID, Kind: kind, Label: e.Text, Parent: it.ID, Line: e.Line})
						}
					}
				case *Child:
					if b.ID != "" {
						out = append(out, Addr{ID: b.ID, Kind: "child", Label: b.Text, Parent: it.ID, Line: b.Line})
					}
				}
			}
		}
	}
	return out
}

func itemLabel(it *Item) string {
	if it.Title != "" {
		return it.Title
	}
	return it.Text
}

// Resolve finds the id a reference names: "#id" must exist, and a title must
// match exactly one titled item (case and spacing aside).
func (d *Doc) Resolve(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "#") {
		id := strings.TrimSpace(ref[1:])
		for _, a := range d.Addressables() {
			if a.ID == id {
				return id, true
			}
		}
		return "", false
	}
	want, found, id := normTitle(ref), 0, ""
	for _, s := range d.Sections {
		for _, n := range s.Nodes {
			if it, ok := n.(*Item); ok && it.Title != "" && it.ID != "" && normTitle(it.Title) == want {
				found++
				id = it.ID
			}
		}
	}
	return id, found == 1
}

// mapRefs rewrites references in all authored text: f gets the inside of each
// "[[...]]" and may return a replacement.
func (d *Doc) mapRefs(f func(inner string) (string, bool)) {
	d.eachText(func(s *string) { *s = rewriteRefs(*s, f) })
}

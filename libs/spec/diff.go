package spec

import (
	"sort"
	"strings"
)

// ChangeOp is what happened to a thing between two revisions.
type ChangeOp string

const (
	Added   ChangeOp = "added"
	Removed ChangeOp = "removed"
	Renamed ChangeOp = "renamed"
	Moved   ChangeOp = "moved"
	// Changed: a value changed (a property, a rule, a field, a setting).
	Changed ChangeOp = "changed"
	// TextChanged: wording changed (a description, a note, the summary).
	TextChanged ChangeOp = "text"
)

// Change is one difference between two documents, found by id.
type Change struct {
	Op ChangeOp `json:"op"`
	// ID is the item, rule or criterion the change is about ("" for the document).
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Parent is the item an entry belongs to.
	Parent string `json:"parent"`
	// Field says what changed: a property name, "title", "text", "description", "fields", "options", "answer".
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
	// Semantic is true when the change alters what the system should be or do,
	// and false when only wording changed (a description, a note, the summary).
	Semantic bool
}

type entryInfo struct {
	id, parent, kind, text string
}

type itemInfo struct {
	doc     *Doc
	it      *Item
	section string
	entries []entryInfo
}

func indexDoc(d *Doc) (order []string, items map[string]*itemInfo, entries map[string]entryInfo) {
	items, entries = map[string]*itemInfo{}, map[string]entryInfo{}
	for _, s := range d.Sections {
		for _, n := range s.Nodes {
			it, ok := n.(*Item)
			if !ok || it.ID == "" {
				continue
			}
			info := &itemInfo{it: it, section: s.Name, doc: d}
			for _, b := range it.Body {
				switch b := b.(type) {
				case *ListProp:
					kind, ok := identifiedLists[b.Key]
					if !ok {
						kind = "child"
					}
					for _, e := range b.Entries {
						if e.ID != "" {
							ei := entryInfo{id: e.ID, parent: it.ID, kind: kind, text: d.normRefs(e.Text)}
							info.entries = append(info.entries, ei)
							entries[e.ID] = ei
						}
					}
				case *Child:
					if b.ID != "" {
						ei := entryInfo{id: b.ID, parent: it.ID, kind: "child", text: d.normRefs(b.Text)}
						info.entries = append(info.entries, ei)
						entries[b.ID] = ei
					}
				}
			}
			items[it.ID] = info
			order = append(order, it.ID)
		}
	}
	return
}

func descriptionOf(d *Doc, it *Item) string {
	var parts []string
	for _, b := range it.Body {
		if p, ok := b.(*Para); ok {
			parts = append(parts, strings.Join(p.Lines, "\n"))
		}
	}
	return d.normRefs(strings.Join(parts, "\n\n"))
}

func propsOf(d *Doc, it *Item) map[string]string {
	m := map[string]string{}
	for _, b := range it.Body {
		switch b := b.(type) {
		case *Prop:
			m[b.Key] = d.normRefs(b.Value)
		case *ListProp:
			if _, ok := identifiedLists[b.Key]; ok {
				continue // its entries are items of their own
			}
			var texts []string
			for _, e := range b.Entries {
				texts = append(texts, e.Text)
			}
			m[b.Key] = d.normRefs(strings.Join(texts, "\n"))
		}
	}
	return m
}

// fieldsOf are an item's bullets without ids (an entity's fields), in order.
func fieldsOf(d *Doc, it *Item) string {
	var texts []string
	for _, b := range it.Body {
		if c, ok := b.(*Child); ok && c.ID == "" {
			texts = append(texts, strings.Repeat("  ", c.Depth)+d.normRefs(c.Text))
		}
	}
	return strings.Join(texts, "\n")
}

func optionsOf(d *Doc, it *Item) string {
	var texts []string
	for _, o := range it.Options {
		texts = append(texts, o.Key+") "+d.normRefs(o.Text))
	}
	return strings.Join(texts, "\n")
}

// Diff lists what changed from a to b, by id. A change is Semantic when it
// alters what the system should be or do, and not when only wording changed:
// that is how a spec edit that changes nothing is told apart from one that needs work.
func Diff(a, b *Doc) []Change {
	var out []Change
	add := func(c Change) { out = append(out, c) }

	// the document itself
	if a.Title != b.Title {
		add(Change{Op: TextChanged, Kind: "document", Field: "title", From: a.Title, To: b.Title})
	}
	if a.Summary != b.Summary {
		add(Change{Op: TextChanged, Kind: "document", Field: "summary", From: a.Summary, To: b.Summary})
	}
	as, bs := map[string]string{}, map[string]string{}
	var keys []string
	for _, s := range a.Settings {
		if _, dup := as[s.Key]; !dup {
			keys = append(keys, s.Key)
		}
		as[s.Key] = s.Value
	}
	for _, s := range b.Settings {
		if _, seen := as[s.Key]; !seen {
			if _, dup := bs[s.Key]; !dup {
				keys = append(keys, s.Key)
			}
		}
		bs[s.Key] = s.Value
	}
	for _, k := range keys {
		av, aok := as[k]
		bv, bok := bs[k]
		switch {
		case aok && !bok:
			add(Change{Op: Removed, Kind: "setting", Label: k, Field: k, From: av, Semantic: true})
		case !aok && bok:
			add(Change{Op: Added, Kind: "setting", Label: k, Field: k, To: bv, Semantic: true})
		case av != bv:
			add(Change{Op: Changed, Kind: "setting", Label: k, Field: k, From: av, To: bv, Semantic: true})
		}
	}

	aOrder, aItems, aEntries := indexDoc(a)
	bOrder, bItems, bEntries := indexDoc(b)

	for _, id := range bOrder {
		bi := bItems[id]
		ai, existed := aItems[id]
		if !existed {
			add(Change{Op: Added, ID: id, Kind: bi.it.Kind, Label: itemLabel(bi.it), Semantic: true})
			continue
		}
		out = append(out, diffItem(id, ai, bi)...)
		// entries of an item that exists in both
		for _, e := range bi.entries {
			old, had := aEntries[e.id]
			switch {
			case !had:
				add(Change{Op: Added, ID: e.id, Kind: e.kind, Label: e.text, Parent: e.parent, Semantic: true})
			case old.parent != e.parent:
				add(Change{Op: Moved, ID: e.id, Kind: e.kind, Label: e.text, Parent: e.parent, From: old.parent, To: e.parent, Semantic: true})
			case old.text != e.text:
				add(Change{Op: Changed, ID: e.id, Kind: e.kind, Label: e.text, Parent: e.parent, Field: "text", From: old.text, To: e.text, Semantic: true})
			}
		}
		for _, e := range ai.entries {
			if _, still := bEntries[e.id]; !still {
				add(Change{Op: Removed, ID: e.id, Kind: e.kind, Label: e.text, Parent: e.parent, Semantic: true})
			}
		}
	}
	for _, id := range aOrder {
		if _, still := bItems[id]; !still {
			ai := aItems[id]
			add(Change{Op: Removed, ID: id, Kind: ai.it.Kind, Label: itemLabel(ai.it), Semantic: true})
		}
	}

	// free text: notes, and the text between the header and the first section
	if x, y := freeText(a.Intro), freeText(b.Intro); x != y {
		add(Change{Op: TextChanged, Kind: "note", Field: "introduction", From: x, To: y})
	}
	secText := func(d *Doc) map[string]string {
		m := map[string]string{}
		for _, s := range d.Sections {
			var parts []string
			for _, n := range s.Nodes {
				if p, ok := n.(*Para); ok {
					parts = append(parts, strings.Join(p.Lines, "\n"))
				}
			}
			if len(parts) > 0 {
				m[s.Name] = strings.Join(parts, "\n\n")
			}
		}
		return m
	}
	at, bt := secText(a), secText(b)
	var names []string
	for n := range at {
		names = append(names, n)
	}
	for n := range bt {
		if _, ok := at[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if at[n] != bt[n] {
			add(Change{Op: TextChanged, Kind: "note", Label: n, Field: "notes", From: at[n], To: bt[n]})
		}
	}
	return out
}

func freeText(nodes []Node) string {
	var parts []string
	for _, n := range nodes {
		if p, ok := n.(*Para); ok {
			parts = append(parts, strings.Join(p.Lines, "\n"))
		}
	}
	return strings.Join(parts, "\n\n")
}

// diffItem compares one item that exists in both documents.
func diffItem(id string, a, b *itemInfo) []Change {
	var out []Change
	x, y := a.it, b.it
	base := Change{ID: id, Kind: y.Kind, Label: itemLabel(y)}
	emit := func(op ChangeOp, field, from, to string, semantic bool) {
		c := base
		c.Op, c.Field, c.From, c.To, c.Semantic = op, field, from, to, semantic
		out = append(out, c)
	}
	if x.Title != y.Title {
		emit(Renamed, "title", x.Title, y.Title, true)
	}
	if a.section != b.section {
		emit(Moved, "section", a.section, b.section, true)
	}
	switch y.Style {
	case StyleQuestion:
		if xt, yt := a.doc.normRefs(x.Text), b.doc.normRefs(y.Text); xt != yt {
			emit(Changed, "text", x.Text, y.Text, true)
		}
		if xo, yo := optionsOf(a.doc, x), optionsOf(b.doc, y); xo != yo {
			emit(Changed, "options", xo, yo, true)
		}
		if x.Answer != y.Answer || x.HasAnswer != y.HasAnswer {
			emit(Changed, "answer", x.Answer, y.Answer, true)
		}
	case StyleBullet:
		if xt, yt := a.doc.normRefs(x.Text), b.doc.normRefs(y.Text); xt != yt {
			// the words after a bold name describe it; on their own they are the content
			emit(textOp(y), "text", x.Text, y.Text, !(y.Named || y.Kind == "note"))
		}
	}
	if x.Style == StyleBlock || y.Style == StyleBlock {
		if xd, yd := descriptionOf(a.doc, x), descriptionOf(b.doc, y); xd != yd {
			emit(TextChanged, "description", xd, yd, false)
		}
		xp, yp := propsOf(a.doc, x), propsOf(b.doc, y)
		var keys []string
		for k := range xp {
			keys = append(keys, k)
		}
		for k := range yp {
			if _, ok := xp[k]; !ok {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			xv, xok := xp[k]
			yv, yok := yp[k]
			switch {
			case xok && !yok:
				emit(Removed, k, xv, "", true)
			case !xok && yok:
				emit(Added, k, "", yv, true)
			case xv != yv:
				emit(Changed, k, xv, yv, true)
			}
		}
	}
	if xf, yf := fieldsOf(a.doc, x), fieldsOf(b.doc, y); xf != yf {
		emit(Changed, "fields", xf, yf, true)
	}
	return out
}

func textOp(it *Item) ChangeOp {
	if it.Named || it.Kind == "note" {
		return TextChanged
	}
	return Changed
}

// Semantic keeps only the changes that alter what the system should be or do.
func Semantic(changes []Change) []Change {
	var out []Change
	for _, c := range changes {
		if c.Semantic {
			out = append(out, c)
		}
	}
	return out
}

// normRefs writes every reference that names exactly one item as that item's
// id, so renaming an item is one change and not a change to every line that
// mentions it.
func (d *Doc) normRefs(s string) string {
	return rewriteRefs(s, func(inner string) (string, bool) {
		if strings.HasPrefix(inner, "#") {
			return "", false
		}
		if ids := d.refMatches(Ref{Inner: inner}); len(ids) == 1 {
			return "#" + ids[0], true
		}
		return "", false
	})
}

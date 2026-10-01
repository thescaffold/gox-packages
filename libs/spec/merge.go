package spec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Conflict is something both sides changed in different ways, which a person has
// to settle. The merged document keeps "mine" for it.
type Conflict struct {
	ID    string
	Kind  string
	Label string
	// Field says what clashed: "title", "text", "description", "prop:uses", "entry:must:c-91ab", "item", "setting:currency", ...
	Field  string
	Reason string
	Base   string
	Mine   string
	Theirs string
}

// Merge3 combines two edits of the same base, item by item. A part of an item
// that only one side changed takes that change; a part both changed the same way
// is taken once; a part both changed differently is a Conflict, and the result keeps
// "mine" there. Order is mine's; items the other side added are placed after the
// item that came before them.
func Merge3(base, mine, theirs *Doc) (*Doc, []Conflict) {
	res := mine.Clone()
	var conflicts []Conflict
	conflict := func(c Conflict) { conflicts = append(conflicts, c) }

	// the document's own words and settings
	pick := func(b, m, t string) (string, bool) {
		switch {
		case m == t || t == b:
			return m, true
		case m == b:
			return t, true
		}
		return m, false
	}
	if v, ok := pick(base.Title, mine.Title, theirs.Title); ok {
		res.Title = v
	} else {
		conflict(Conflict{Kind: "document", Field: "title", Reason: "You and they both renamed the document.", Base: base.Title, Mine: mine.Title, Theirs: theirs.Title})
	}
	if v, ok := pick(base.Summary, mine.Summary, theirs.Summary); ok {
		res.Summary = v
	} else {
		conflict(Conflict{Kind: "document", Field: "summary", Reason: "You and they both rewrote the summary.", Base: base.Summary, Mine: mine.Summary, Theirs: theirs.Summary})
	}
	mergeSettings(base, mine, theirs, res, conflict)

	_, bItems, _ := indexDoc(base)
	_, mItems, _ := indexDoc(mine)
	tOrder, tItems, _ := indexDoc(theirs)

	resItems := func() map[string]*Item {
		m := map[string]*Item{}
		for _, s := range res.Sections {
			for _, n := range s.Nodes {
				if it, ok := n.(*Item); ok && it.ID != "" {
					m[it.ID] = it
				}
			}
		}
		return m
	}

	// items in base
	baseIDs := make([]string, 0, len(bItems))
	for id := range bItems {
		baseIDs = append(baseIDs, id)
	}
	sort.Strings(baseIDs)
	for _, id := range baseIDs {
		bi, mi, ti := bItems[id], mItems[id], tItems[id]
		switch {
		case mi == nil && ti == nil:
		case ti == nil: // they deleted it
			if itemText(mi.it) == itemText(bi.it) {
				removeFromDoc(res, id)
			} else {
				conflict(Conflict{ID: id, Kind: mi.it.Kind, Label: itemLabel(mi.it), Field: "item",
					Reason: "They deleted this and you changed it.", Base: itemText(bi.it), Mine: itemText(mi.it)})
			}
		case mi == nil: // you deleted it
			if itemText(ti.it) != itemText(bi.it) {
				conflict(Conflict{ID: id, Kind: ti.it.Kind, Label: itemLabel(ti.it), Field: "item",
					Reason: "You deleted this and they changed it.", Base: itemText(bi.it), Theirs: itemText(ti.it)})
			}
		default:
			ri := resItems()[id]
			conflicts = append(conflicts, mergeItem(base, mine, theirs, res, bi.it, mi.it, ti.it, ri)...)
			switch {
			case bi.section == mi.section && ti.section != bi.section:
				moveToSection(res, id, ti.section, theirs)
			case bi.section != mi.section && ti.section != bi.section && ti.section != mi.section:
				conflict(Conflict{ID: id, Kind: ri.Kind, Label: itemLabel(ri), Field: "section",
					Reason: "You and they moved this to different sections.", Base: bi.section, Mine: mi.section, Theirs: ti.section})
			}
		}
	}

	// items they added
	for _, id := range tOrder {
		if _, inBase := bItems[id]; inBase {
			continue
		}
		ti := tItems[id]
		if mi, both := mItems[id]; both {
			if itemText(mi.it) != itemText(ti.it) {
				conflict(Conflict{ID: id, Kind: ti.it.Kind, Label: itemLabel(ti.it), Field: "item",
					Reason: "You and they each added a different item with the id “" + id + "”.", Mine: itemText(mi.it), Theirs: itemText(ti.it)})
			}
			continue
		}
		insertAfterPredecessor(res, ti.it.Clone(), ti.section, theirs, id)
	}

	// free text in notes
	mergeFreeText(base, mine, theirs, res, conflict)
	// ids made independently on the two sides can collide; the later copy gets a new one
	Canonicalize(res, nil)
	return res, conflicts
}

// Clone copies an item.
func (it *Item) Clone() *Item { return cloneNode(it).(*Item) }

func mergeSettings(base, mine, theirs, res *Doc, conflict func(Conflict)) {
	get := func(d *Doc) (map[string]string, []string) {
		m := map[string]string{}
		var keys []string
		for _, s := range d.Settings {
			if _, ok := m[s.Key]; !ok {
				keys = append(keys, s.Key)
			}
			m[s.Key] = s.Value
		}
		return m, keys
	}
	bm, _ := get(base)
	mm, mk := get(mine)
	tm, tk := get(theirs)
	seen := map[string]bool{}
	var keys []string
	for _, k := range append(append([]string{}, mk...), tk...) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		bv, bok := bm[k]
		mv, mok := mm[k]
		tv, tok := tm[k]
		switch {
		case mok == tok && mv == tv:
		case mok == bok && mv == bv: // only they changed it
			setSetting(res, k, tv, tok)
		case tok == bok && tv == bv:
		default:
			conflict(Conflict{Kind: "setting", Label: k, Field: "setting:" + k, Reason: "You and they both changed the setting “" + k + "”.", Base: bv, Mine: mv, Theirs: tv})
		}
	}
}

func setSetting(d *Doc, key, val string, present bool) {
	for i, s := range d.Settings {
		if s.Key == key {
			if present {
				d.Settings[i].Value = val
			} else {
				d.Settings = append(d.Settings[:i:i], d.Settings[i+1:]...)
			}
			return
		}
	}
	if present {
		d.Settings = append(d.Settings, Setting{Key: key, Value: val})
	}
}

func removeFromDoc(d *Doc, id string) {
	for _, s := range d.Sections {
		for i, n := range s.Nodes {
			if it, ok := n.(*Item); ok && it.ID == id {
				s.Nodes = removeNode(s.Nodes, i)
				return
			}
		}
	}
}

// sectionFor finds a section by name, making it (at the end) when it is not there.
func (d *Doc) sectionFor(name string) *Section {
	if s := d.findSection(name); s != nil {
		return s
	}
	for _, canon := range sectionNames {
		if strings.EqualFold(canon, collapse(name)) {
			s := &Section{Name: canon, Known: true, Kind: sectionKinds[strings.ToLower(canon)]}
			d.Sections = append(d.Sections, s)
			return s
		}
	}
	s := &Section{Name: name, Kind: "note"}
	d.Sections = append(d.Sections, s)
	return s
}

// insertAfterPredecessor puts item into the section called secName, after the
// nearest item that came before it in other's section and is also in res.
func insertAfterPredecessor(res *Doc, item *Item, secName string, other *Doc, id string) {
	sec := res.sectionFor(secName)
	at := 0
	if os := other.findSection(secName); os != nil {
		var before []string
		for _, n := range os.Nodes {
			if it, ok := n.(*Item); ok {
				if it.ID == id {
					break
				}
				before = append(before, it.ID)
			}
		}
		at = -1
		for i := len(before) - 1; i >= 0 && at < 0; i-- {
			if j := sec.itemIndex(before[i]); j >= 0 {
				at = j + 1
			}
		}
		if at < 0 {
			at = 0
			for i, n := range sec.Nodes {
				if _, ok := n.(*Item); ok {
					at = i
					break
				}
				at = i + 1
			}
		}
	} else {
		at = len(sec.Nodes)
	}
	sec.Nodes = insertNode(sec.Nodes, at, item)
}

func moveToSection(res *Doc, id, secName string, other *Doc) {
	var it *Item
	for _, s := range res.Sections {
		for i, n := range s.Nodes {
			if x, ok := n.(*Item); ok && x.ID == id {
				it = x
				s.Nodes = removeNode(s.Nodes, i)
				break
			}
		}
		if it != nil {
			break
		}
	}
	if it != nil {
		insertAfterPredecessor(res, it, secName, other, id)
	}
}

func mergeFreeText(base, mine, theirs, res *Doc, conflict func(Conflict)) {
	text := func(d *Doc, name string) string {
		if s := d.findSection(name); s != nil {
			var parts []string
			for _, n := range s.Nodes {
				if p, ok := n.(*Para); ok {
					parts = append(parts, strings.Join(p.Lines, "\n"))
				}
			}
			return strings.Join(parts, "\n\n")
		}
		return ""
	}
	seen := map[string]bool{}
	var names []string
	for _, d := range []*Doc{base, mine, theirs} {
		for _, s := range d.Sections {
			if !seen[s.Name] {
				seen[s.Name] = true
				names = append(names, s.Name)
			}
		}
	}
	for _, name := range names {
		b, m, t := text(base, name), text(mine, name), text(theirs, name)
		switch {
		case m == t || t == b:
		case m == b:
			sec := res.sectionFor(name)
			kept := sec.Nodes[:0:0]
			for _, n := range sec.Nodes {
				if _, ok := n.(*Para); !ok {
					kept = append(kept, n)
				}
			}
			sec.Nodes = kept
			if t != "" {
				for _, para := range strings.Split(t, "\n\n") {
					sec.Nodes = append(sec.Nodes, &Para{Lines: strings.Split(para, "\n")})
				}
			}
		default:
			conflict(Conflict{Kind: "note", Label: name, Field: "notes", Reason: "You and they both rewrote the notes in “" + name + "”.", Base: b, Mine: m, Theirs: t})
		}
	}
	if b, m, t := freeText(base.Intro), freeText(mine.Intro), freeText(theirs.Intro); !(m == t || t == b) {
		if m == b {
			res.Intro = cloneNodes(theirs.Intro)
		} else {
			conflict(Conflict{Kind: "note", Field: "introduction", Reason: "You and they both rewrote the introduction.", Base: b, Mine: m, Theirs: t})
		}
	}
}

// ---- merging one item ----

// slotVals reads an item as a set of named values, so each can be merged on its own.
func slotVals(d *Doc, it *Item, normalize bool) map[string]string {
	n := func(s string) string { return s }
	if normalize {
		n = d.normRefs
	}
	m := map[string]string{"title": it.Title, "text": n(it.Text), "cont": n(strings.Join(it.Cont, "\n")), "description": n(descriptionRaw(it))}
	if it.Style == StyleQuestion {
		if it.HasAnswer {
			m["answer"] = it.Answer
		}
		var o []string
		for _, x := range it.Options {
			o = append(o, x.Key+"\t"+x.Text)
		}
		m["options"] = n(strings.Join(o, "\n"))
	}
	var fields []string
	idless := map[string][]string{}
	for _, b := range it.Body {
		switch b := b.(type) {
		case *Prop:
			m["prop:"+b.Key] = n(b.Value)
		case *ListProp:
			for _, e := range b.Entries {
				if e.ID != "" {
					m["entry:"+b.Key+":"+e.ID] = strconv.Itoa(int(e.Box)) + "|" + n(e.Text)
				} else {
					idless[b.Key] = append(idless[b.Key], strconv.Itoa(int(e.Box))+"|"+n(e.Text))
				}
			}
		case *Child:
			if b.ID != "" {
				m["child:"+b.ID] = strconv.Itoa(b.Depth) + "|" + n(b.Text) + "\x00" + n(strings.Join(b.Cont, "\n"))
			} else {
				fields = append(fields, strconv.Itoa(b.Depth)+"|"+n(b.Text)+"\x00"+n(strings.Join(b.Cont, "\n")))
			}
		}
	}
	for k, v := range idless {
		m["list:"+k] = strings.Join(v, "\n")
	}
	if len(fields) > 0 {
		m["fields"] = strings.Join(fields, "\n")
	}
	return m
}

func descriptionRaw(it *Item) string {
	var parts []string
	for _, b := range it.Body {
		if p, ok := b.(*Para); ok {
			parts = append(parts, strings.Join(p.Lines, "\n"))
		}
	}
	return strings.Join(parts, "\n\n")
}

func setSlot(it *Item, key, val string, present bool) {
	switch {
	case key == "title":
		it.Title = val
	case key == "text":
		it.Text = val
	case key == "cont":
		if val == "" {
			it.Cont = nil
		} else {
			it.Cont = strings.Split(val, "\n")
		}
	case key == "description":
		first, kept := -1, it.Body[:0:0]
		for _, b := range it.Body {
			if _, ok := b.(*Para); ok {
				if first < 0 {
					first = len(kept)
				}
				continue
			}
			kept = append(kept, b)
		}
		it.Body = kept
		if val != "" {
			if first < 0 {
				first = 0
			}
			var paras []Node
			for _, para := range strings.Split(val, "\n\n") {
				paras = append(paras, &Para{Lines: strings.Split(para, "\n")})
			}
			body := append([]Node{}, it.Body[:first]...)
			body = append(body, paras...)
			it.Body = append(body, it.Body[first:]...)
		}
	case key == "answer":
		it.Answer, it.HasAnswer = val, present
	case key == "options":
		it.Options = nil
		if val != "" {
			for _, line := range strings.Split(val, "\n") {
				k, txt, _ := strings.Cut(line, "\t")
				it.Options = append(it.Options, Option{Key: k, Text: txt})
			}
		}
	case strings.HasPrefix(key, "prop:"):
		k := key[len("prop:"):]
		for i, b := range it.Body {
			if p, ok := b.(*Prop); ok && p.Key == k {
				if present {
					p.Value = val
				} else {
					it.Body = removeNode(it.Body, i)
				}
				return
			}
		}
		if present {
			at := len(it.Body)
			for i, b := range it.Body {
				switch b.(type) {
				case *ListProp, *Child:
					at = i
				}
				if at != len(it.Body) {
					break
				}
			}
			it.Body = insertNode(it.Body, at, &Prop{Key: k, Value: val})
		}
	case strings.HasPrefix(key, "entry:"):
		rest := key[len("entry:"):]
		cut := strings.LastIndex(rest, ":")
		listKey, id := rest[:cut], rest[cut+1:]
		var lp *ListProp
		for _, b := range it.Body {
			if l, ok := b.(*ListProp); ok && l.Key == listKey {
				lp = l
			}
		}
		if present {
			boxS, txt, _ := strings.Cut(val, "|")
			box, _ := strconv.Atoi(boxS)
			if lp == nil {
				lp = &ListProp{Key: listKey}
				it.Body = append(it.Body, lp)
			}
			for _, e := range lp.Entries {
				if e.ID == id {
					e.Text, e.Box = txt, Box(box)
					return
				}
			}
			lp.Entries = append(lp.Entries, &Entry{ID: id, Text: txt, Box: Box(box)})
			return
		}
		if lp != nil {
			for i, e := range lp.Entries {
				if e.ID == id {
					lp.Entries = append(lp.Entries[:i:i], lp.Entries[i+1:]...)
					break
				}
			}
			if len(lp.Entries) == 0 {
				for i, b := range it.Body {
					if b == Node(lp) {
						it.Body = removeNode(it.Body, i)
						break
					}
				}
			}
		}
	case strings.HasPrefix(key, "list:"):
		k := key[len("list:"):]
		for i, b := range it.Body {
			if l, ok := b.(*ListProp); ok && l.Key == k {
				var keep []*Entry
				for _, e := range l.Entries {
					if e.ID != "" {
						keep = append(keep, e)
					}
				}
				l.Entries = keep
				if present && val != "" {
					for _, line := range strings.Split(val, "\n") {
						boxS, txt, _ := strings.Cut(line, "|")
						box, _ := strconv.Atoi(boxS)
						l.Entries = append(l.Entries, &Entry{Text: txt, Box: Box(box)})
					}
				}
				if len(l.Entries) == 0 {
					it.Body = removeNode(it.Body, i)
				}
				return
			}
		}
		if present && val != "" {
			l := &ListProp{Key: k}
			for _, line := range strings.Split(val, "\n") {
				boxS, txt, _ := strings.Cut(line, "|")
				box, _ := strconv.Atoi(boxS)
				l.Entries = append(l.Entries, &Entry{Text: txt, Box: Box(box)})
			}
			it.Body = append(it.Body, l)
		}
	case strings.HasPrefix(key, "child:"):
		id := key[len("child:"):]
		for i, b := range it.Body {
			if c, ok := b.(*Child); ok && c.ID == id {
				if !present {
					it.Body = removeNode(it.Body, i)
					return
				}
				c.Depth, c.Text, c.Cont = parseChild(val)
				return
			}
		}
		if present {
			c := &Child{ID: id}
			c.Depth, c.Text, c.Cont = parseChild(val)
			it.Body = append(it.Body, c)
		}
	case key == "fields":
		var kept []Node
		for _, b := range it.Body {
			if c, ok := b.(*Child); ok && c.ID == "" {
				continue
			}
			kept = append(kept, b)
		}
		it.Body = kept
		if present && val != "" {
			for _, line := range strings.Split(val, "\n") {
				c := &Child{}
				c.Depth, c.Text, c.Cont = parseChild(line)
				it.Body = append(it.Body, c)
			}
		}
	}
}

func parseChild(v string) (depth int, text string, cont []string) {
	d, rest, _ := strings.Cut(v, "|")
	depth, _ = strconv.Atoi(d)
	text, c, _ := strings.Cut(rest, "\x00")
	if c != "" {
		cont = strings.Split(c, "\n")
	}
	return
}

// mergeItem folds the changes they made to one item into res, which starts as mine.
func mergeItem(bd, md, td, rd *Doc, b, m, t, res *Item) []Conflict {
	var out []Conflict
	if m.Style != t.Style || b.Style != m.Style && b.Style != t.Style {
		if itemText(m) != itemText(t) {
			out = append(out, Conflict{ID: res.ID, Kind: res.Kind, Label: itemLabel(res), Field: "item", Reason: "You and they wrote this item in different forms.",
				Base: itemText(b), Mine: itemText(m), Theirs: itemText(t)})
		}
		return out
	}
	bv, mv, tv := slotVals(bd, b, true), slotVals(md, m, true), slotVals(td, t, true)
	tRaw := slotVals(td, t, false)
	seen := map[string]bool{}
	var keys []string
	for _, src := range []map[string]string{tv, mv, bv} {
		var ks []string
		for k := range src {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	for _, k := range keys {
		bval, bok := bv[k]
		mval, mok := mv[k]
		tval, tok := tv[k]
		switch {
		case mok == tok && mval == tval:
		case mok == bok && mval == bval: // only they changed it
			if k == "title" {
				rd.retitle(res, tRaw[k])
			} else {
				setSlot(res, k, translateRefs(tRaw[k], td, rd), tok)
			}
		case tok == bok && tval == bval: // only I did
		default:
			out = append(out, Conflict{ID: res.ID, Kind: res.Kind, Label: itemLabel(res), Field: k,
				Reason: fmt.Sprintf("You and they both changed %s of “%s”.", slotName(k), itemLabel(res)), Base: bval, Mine: mval, Theirs: tval})
		}
	}
	return out
}

func slotName(k string) string {
	switch {
	case k == "title":
		return "the title"
	case k == "text":
		return "the text"
	case k == "description":
		return "the description"
	case strings.HasPrefix(k, "prop:"):
		return "“" + k[len("prop:"):] + "”"
	case strings.HasPrefix(k, "entry:"):
		return "a rule or check"
	case strings.HasPrefix(k, "child:"), k == "fields":
		return "a field"
	}
	return k
}

// translateRefs rewrites the references in text written against one document so
// they name the same things by the titles the other document uses.
func translateRefs(text string, from, to *Doc) string {
	return rewriteRefs(text, func(inner string) (string, bool) {
		if strings.HasPrefix(inner, "#") {
			return "", false
		}
		ids := from.refMatches(Ref{Inner: inner})
		if len(ids) != 1 {
			return "", false
		}
		if l, ok := to.locate(ids[0]); ok && l.item != nil && l.item.Title != "" {
			return l.item.Title, true
		}
		return "#" + ids[0], true
	})
}

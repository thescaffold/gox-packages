package spec

import (
	"fmt"
	"sort"
	"strings"
)

// Apply makes the changes a patch describes and returns the new document. It is
// atomic: the original is never touched, and if any step fails, nothing is
// returned but the error. New items without an id are given one.
func Apply(d *Doc, p SpecPatch) (*Doc, error) {
	nd := d.Clone()
	for i, op := range p.Ops {
		if msg := op.shapeProblem(); msg != "" {
			return nil, &PatchError{Step: i, Op: op.Op, Msg: msg}
		}
		if err := nd.applyOp(op); err != nil {
			return nil, &PatchError{Step: i, Op: op.Op, Msg: err.Error()}
		}
	}
	if err := structuralError(nd); err != nil {
		return nil, fmt.Errorf("the patch would leave the spec broken: %v", err)
	}
	Canonicalize(nd, nil)
	return nd, nil
}

// structuralError is the first thing that must be fixed before a document can be committed.
func structuralError(d *Doc) error {
	for _, dg := range Validate(d) {
		if dg.Severity == Error {
			return fmt.Errorf("%s", dg.Message)
		}
	}
	return nil
}

func (d *Doc) applyOp(op PatchOp) error {
	switch op.Op {
	case "add":
		return d.opAdd(op)
	case "set":
		return d.opSet(op)
	case "text":
		return d.opText(op)
	case "answer":
		return d.opAnswer(op)
	case "rename":
		return d.opRename(op)
	case "remove":
		return d.opRemove(op)
	case "move":
		return d.opMove(op)
	case "split":
		return d.opSplit(op)
	case "merge":
		return d.opMerge(op)
	case "replace":
		return d.opReplace(op)
	case "restore":
		return d.opRestore(op)
	case "prune":
		return d.opPrune(op)
	case "section":
		return d.opSection(op)
	}
	return fmt.Errorf("“%s” is not something a patch can do", op.Op)
}

// ---- finding things ----

type loc struct {
	sec  *Section
	idx  int   // node index in sec.Nodes (for an item)
	item *Item // a top-level item
	// for a rule, criterion or field inside an item
	owner *Item
	list  *ListProp
	entry *Entry
	child *Child
	pos   int // index in owner.Body (child or list) or in list.Entries (entry)
}

func (d *Doc) locate(id string) (loc, bool) {
	if id == "" {
		return loc{}, false
	}
	for _, s := range d.Sections {
		for i, n := range s.Nodes {
			it, ok := n.(*Item)
			if !ok {
				continue
			}
			if it.ID == id {
				return loc{sec: s, idx: i, item: it}, true
			}
			for bi, b := range it.Body {
				switch b := b.(type) {
				case *ListProp:
					for ei, e := range b.Entries {
						if e.ID == id {
							return loc{sec: s, idx: i, owner: it, list: b, entry: e, pos: ei}, true
						}
					}
				case *Child:
					if b.ID == id {
						return loc{sec: s, idx: i, owner: it, child: b, pos: bi}, true
					}
				}
			}
		}
	}
	return loc{}, false
}

func (d *Doc) idTaken(id string) bool {
	_, ok := d.locate(id)
	return ok
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func (d *Doc) findSection(name string) *Section {
	for _, s := range d.Sections {
		if strings.EqualFold(collapse(s.Name), collapse(name)) {
			return s
		}
	}
	return nil
}

// ensureSection finds a section by name or, for one of the standard sections, creates it at the end.
func (d *Doc) ensureSection(name string) (*Section, error) {
	if s := d.findSection(name); s != nil {
		return s, nil
	}
	for _, canon := range sectionNames {
		if strings.EqualFold(canon, collapse(name)) {
			s := &Section{Name: canon, Known: true, Kind: sectionKinds[strings.ToLower(canon)]}
			d.Sections = append(d.Sections, s)
			return s, nil
		}
	}
	return nil, fmt.Errorf("there is no section called “%s” (the sections are %s)", name, strings.Join(sectionNames, ", "))
}

func (s *Section) itemIndex(id string) int {
	for i, n := range s.Nodes {
		if it, ok := n.(*Item); ok && it.ID == id {
			return i
		}
	}
	return -1
}

func insertNode(nodes []Node, at int, n Node) []Node {
	nodes = append(nodes, nil)
	copy(nodes[at+1:], nodes[at:])
	nodes[at] = n
	return nodes
}

func removeNode(nodes []Node, at int) []Node {
	return append(nodes[:at:at], nodes[at+1:]...)
}

// itemPosition turns "after" into an index in the section's nodes.
func (s *Section) itemPosition(after *string) (int, error) {
	if after == nil {
		return len(s.Nodes), nil
	}
	if *after == "" {
		for i, n := range s.Nodes {
			if _, ok := n.(*Item); ok {
				return i, nil
			}
		}
		return len(s.Nodes), nil
	}
	i := s.itemIndex(*after)
	if i < 0 {
		return 0, fmt.Errorf("“%s” is not in %s, so nothing can be placed after it", *after, s.Name)
	}
	return i + 1, nil
}

// ---- building items ----

func defaultStyle(kind string) Style {
	switch kind {
	case "question":
		return StyleQuestion
	case "feature", "entity", "screen", "part", "environment":
		return StyleBlock
	}
	return StyleBullet
}

var propOrder = []string{"for", "priority", "needs", "uses", "shows", "does", "owns", "talks to", "runs as", "provider", "used for", "needs keys",
	"purpose", "domain", "protected", "can", "notes", "device", "actions", "kept"}

func propRank(k string) int {
	for i, p := range propOrder {
		if p == k {
			return i
		}
	}
	return len(propOrder)
}

func oneLine(what, s string) error {
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("%s must be a single line", what)
	}
	return nil
}

func joinList(vs []string) string {
	var parts []string
	for _, v := range vs {
		parts = append(parts, splitList(v)...)
	}
	return strings.Join(parts, ", ")
}

// splitList splits "[[A]], [[B]], c" at the commas outside [[ ]].
func splitList(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch {
		case strings.HasPrefix(s[i:], "[["):
			depth++
			i++
		case strings.HasPrefix(s[i:], "]]") && depth > 0:
			depth--
			i++
		case s[i] == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	out = append(out, strings.TrimSpace(s[start:]))
	var keep []string
	for _, v := range out {
		if v != "" {
			keep = append(keep, v)
		}
	}
	return keep
}

func (d *Doc) newItem(kind string, op PatchOp) (*Item, error) {
	if op.ID != "" {
		if !validID(op.ID) {
			return nil, fmt.Errorf("“%s” cannot be an id (ids are letters, numbers, - and _)", op.ID)
		}
		if d.idTaken(op.ID) {
			return nil, fmt.Errorf("the id “%s” is already used", op.ID)
		}
	}
	style := defaultStyle(kind)
	switch op.Style {
	case "":
	case "block":
		if kind != "question" {
			style = StyleBlock
		}
	case "bullet":
		if kind != "question" {
			style = StyleBullet
		}
	default:
		return nil, fmt.Errorf("the style “%s” is not block or bullet", op.Style)
	}
	it := &Item{Kind: kind, Style: style, ID: op.ID}
	title, text := strings.TrimSpace(op.Title), strings.TrimSpace(op.Text)
	if err := oneLine("the title", title); err != nil {
		return nil, err
	}
	if style != StyleBlock {
		if err := oneLine("the text", text); err != nil {
			return nil, err
		}
	}
	switch style {
	case StyleQuestion:
		if text == "" {
			return nil, fmt.Errorf("a question needs its text")
		}
		it.Text = text
		for i, o := range op.Options {
			if strings.TrimSpace(o) == "" || oneLine("an option", o) != nil {
				return nil, fmt.Errorf("an option must be one line of text")
			}
			it.Options = append(it.Options, Option{Key: string(rune('a' + i)), Text: strings.TrimSpace(o)})
		}
	case StyleBlock:
		if title == "" {
			return nil, fmt.Errorf("a %s needs a title", kind)
		}
		it.Title = title
		if text != "" {
			it.Body = append(it.Body, &Para{Lines: strings.Split(text, "\n")})
		}
		keys := make([]string, 0, len(op.Props))
		for k := range op.Props {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			ri, rj := propRank(keys[i]), propRank(keys[j])
			if ri != rj {
				return ri < rj
			}
			return keys[i] < keys[j]
		})
		for _, k := range keys {
			if !validPropName(k) {
				return nil, fmt.Errorf("“%s” cannot be a property name (one to three lowercase words)", k)
			}
			vals := op.Props[k]
			if _, listy := identifiedLists[k]; listy {
				lp := &ListProp{Key: k}
				for _, v := range vals {
					if strings.TrimSpace(v) == "" || oneLine("a list entry", v) != nil {
						return nil, fmt.Errorf("a list entry must be one line of text")
					}
					e := &Entry{Text: strings.TrimSpace(v)}
					if k == "done when" {
						e.Box = BoxOpen
					}
					lp.Entries = append(lp.Entries, e)
				}
				if len(lp.Entries) > 0 {
					it.Body = append(it.Body, lp)
				}
				continue
			}
			if v := joinList(vals); v != "" {
				if oneLine("a property", v) != nil {
					return nil, fmt.Errorf("a property must be a single line")
				}
				it.Body = append(it.Body, &Prop{Key: k, Value: v})
			}
		}
	default:
		if len(op.Props) > 0 {
			return nil, fmt.Errorf("a %s is written as one line and cannot carry properties; make it a block item or put the detail in its text", kind)
		}
		switch {
		case title != "":
			it.Title, it.Named, it.Text = title, true, text
		case kind == "user" || kind == "integration":
			return nil, fmt.Errorf("a %s needs a name (title)", kind)
		case text == "":
			return nil, fmt.Errorf("a %s needs its text", kind)
		default:
			it.Text = text
		}
	}
	return it, nil
}

// ---- operations ----

func (d *Doc) opAdd(op PatchOp) error {
	if l, ok := d.locate(op.Parent); ok && l.item != nil {
		return d.addEntry(l.item, op)
	}
	sec, err := d.ensureSection(op.Parent)
	if err != nil {
		return fmt.Errorf("“%s” is neither a section nor an item", op.Parent)
	}
	if op.Kind != "" && op.Kind != sec.Kind {
		return fmt.Errorf("%s holds %ss, not %ss", sec.Name, sec.Kind, op.Kind)
	}
	it, err := d.newItem(sec.Kind, op)
	if err != nil {
		return err
	}
	at, err := sec.itemPosition(op.After)
	if err != nil {
		return err
	}
	sec.Nodes = insertNode(sec.Nodes, at, it)
	return nil
}

// addEntry adds a rule, criterion, deferral or field to an item.
func (d *Doc) addEntry(it *Item, op PatchOp) error {
	text := strings.TrimSpace(op.Text)
	if text == "" || oneLine("it", text) != nil {
		return fmt.Errorf("it needs one line of text")
	}
	if op.ID != "" {
		if !validID(op.ID) {
			return fmt.Errorf("“%s” cannot be an id", op.ID)
		}
		if d.idTaken(op.ID) {
			return fmt.Errorf("the id “%s” is already used", op.ID)
		}
	}
	if op.List == "fields" || op.List == "field" {
		if it.Style == StyleQuestion {
			return fmt.Errorf("a question has no fields")
		}
		ch := &Child{Text: text, ID: op.ID}
		at := len(it.Body)
		if op.After != nil {
			at = 0
			if *op.After != "" {
				found := false
				for i, b := range it.Body {
					if c, ok := b.(*Child); ok && c.ID == *op.After {
						at, found = i+1, true
					}
				}
				if !found {
					return fmt.Errorf("“%s” is not a field of %s", *op.After, it.ID)
				}
			}
		}
		it.Body = insertNode(it.Body, at, ch)
		return nil
	}
	if _, ok := identifiedLists[op.List]; !ok {
		return fmt.Errorf("a list is “must”, “done when”, “not now” or “fields”, not “%s”", op.List)
	}
	if it.Style != StyleBlock {
		return fmt.Errorf("only an item written as a block can have a “%s” list", op.List)
	}
	var lp *ListProp
	for _, b := range it.Body {
		if l, ok := b.(*ListProp); ok && l.Key == op.List {
			lp = l
		}
	}
	if lp == nil {
		lp = &ListProp{Key: op.List}
		it.Body = append(it.Body, lp)
	}
	e := &Entry{Text: text, ID: op.ID}
	if op.List == "done when" {
		e.Box = BoxOpen
	}
	at := len(lp.Entries)
	if op.After != nil {
		at = 0
		if *op.After != "" {
			at = -1
			for i, x := range lp.Entries {
				if x.ID == *op.After {
					at = i + 1
				}
			}
			if at < 0 {
				return fmt.Errorf("“%s” is not in the “%s” list", *op.After, op.List)
			}
		}
	}
	lp.Entries = append(lp.Entries, nil)
	copy(lp.Entries[at+1:], lp.Entries[at:])
	lp.Entries[at] = e
	return nil
}

// sameRef reports whether two list elements name the same thing: the same
// reference target, or the same words.
func (d *Doc) sameRef(a, b string) bool {
	if normTitle(a) == normTitle(b) {
		return true
	}
	ia, oka := d.refTarget(a)
	ib, okb := d.refTarget(b)
	return oka && okb && ia == ib
}

// refTarget resolves an element that is exactly one "[[...]]".
func (d *Doc) refTarget(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[[") || !strings.HasSuffix(s, "]]") {
		return "", false
	}
	inner := strings.TrimSpace(s[2 : len(s)-2])
	if inner == "" || strings.ContainsAny(inner, "[]") {
		return "", false
	}
	return d.Resolve(inner)
}

func (d *Doc) topItem(id string) (*Item, loc, error) {
	l, ok := d.locate(id)
	if !ok || l.item == nil {
		if ok {
			return nil, l, fmt.Errorf("“%s” is a part of an item, not an item", id)
		}
		return nil, l, fmt.Errorf("there is no item “%s”", id)
	}
	return l.item, l, nil
}

func (d *Doc) opSet(op PatchOp) error {
	it, _, err := d.topItem(op.Item)
	if err != nil {
		return err
	}
	if it.Style != StyleBlock {
		return fmt.Errorf("“%s” is written as one line and has no properties to set", op.Item)
	}
	key := strings.TrimSpace(op.Prop)
	if key == "" {
		return fmt.Errorf("it does not say which property")
	}
	var prop *Prop
	pi := -1
	for i, b := range it.Body {
		switch b := b.(type) {
		case *Prop:
			if b.Key == key {
				prop, pi = b, i
			}
		case *ListProp:
			if b.Key == key {
				return fmt.Errorf("“%s” is a list; add and remove its entries one by one", key)
			}
		}
	}
	if _, listy := identifiedLists[key]; listy {
		return fmt.Errorf("“%s” is a list; add and remove its entries one by one", key)
	}
	var elems []string
	if prop != nil {
		elems = splitList(prop.Value)
	}
	switch {
	case op.Value != nil:
		elems = splitList(joinList(*op.Value))
	default:
		for _, r := range op.Remove {
			found := false
			kept := elems[:0:0]
			for _, e := range elems {
				if d.sameRef(e, r) {
					found = true
					continue
				}
				kept = append(kept, e)
			}
			if !found {
				return fmt.Errorf("“%s” is not in “%s” of %s", r, key, op.Item)
			}
			elems = kept
		}
		for _, a := range op.Add {
			for _, v := range splitList(a) {
				dup := false
				for _, e := range elems {
					if d.sameRef(e, v) {
						dup = true
					}
				}
				if !dup {
					elems = append(elems, v)
				}
			}
		}
	}
	val := strings.Join(elems, ", ")
	if oneLine("a property", val) != nil {
		return fmt.Errorf("a property must be a single line")
	}
	switch {
	case val == "" && prop != nil:
		it.Body = removeNode(it.Body, pi)
	case val == "":
	case prop != nil:
		prop.Value = val
	default:
		if !validPropName(key) {
			return fmt.Errorf("“%s” cannot be a property name (one to three lowercase words)", key)
		}
		at := len(it.Body)
		for i, b := range it.Body {
			if _, ok := b.(*ListProp); ok {
				at = i
				break
			}
			if _, ok := b.(*Child); ok {
				at = i
				break
			}
		}
		it.Body = insertNode(it.Body, at, &Prop{Key: key, Value: val})
	}
	return nil
}

func (d *Doc) opText(op PatchOp) error {
	l, ok := d.locate(op.Item)
	if !ok {
		return fmt.Errorf("there is no item “%s”", op.Item)
	}
	text := strings.TrimSpace(op.Text)
	switch {
	case l.entry != nil:
		if text == "" || oneLine("it", text) != nil {
			return fmt.Errorf("it needs one line of text")
		}
		l.entry.Text = text
	case l.child != nil:
		if text == "" || oneLine("it", text) != nil {
			return fmt.Errorf("it needs one line of text")
		}
		l.child.Text = text
	case l.item.Style == StyleQuestion:
		if text == "" || oneLine("a question", text) != nil {
			return fmt.Errorf("a question needs one line of text")
		}
		l.item.Text = text
	case l.item.Style == StyleBullet:
		if oneLine("it", text) != nil {
			return fmt.Errorf("it must be a single line")
		}
		if text == "" && !l.item.Named {
			return fmt.Errorf("it needs some text")
		}
		l.item.Text = text
	default: // the description of a block item
		it := l.item
		for i, b := range it.Body {
			if p, ok := b.(*Para); ok {
				if text == "" {
					it.Body = removeNode(it.Body, i)
				} else {
					p.Lines = strings.Split(text, "\n")
				}
				return nil
			}
		}
		if text != "" {
			it.Body = insertNode(it.Body, 0, &Para{Lines: strings.Split(text, "\n")})
		}
	}
	return nil
}

func (d *Doc) opAnswer(op PatchOp) error {
	it, _, err := d.topItem(op.Item)
	if err != nil {
		return err
	}
	if it.Style != StyleQuestion {
		return fmt.Errorf("“%s” is not a question", op.Item)
	}
	if op.Answer == nil {
		return fmt.Errorf("it does not say the answer")
	}
	a := strings.TrimSpace(*op.Answer)
	if oneLine("an answer", a) != nil {
		return fmt.Errorf("an answer must be a single line")
	}
	if a == "" {
		it.Answer, it.HasAnswer = "", false
		return nil
	}
	if len(it.Options) > 0 {
		ok := false
		for _, o := range it.Options {
			if strings.EqualFold(o.Key, a) {
				a, ok = o.Key, true
			}
		}
		if !ok {
			return fmt.Errorf("“%s” is not one of the choices", a)
		}
	}
	it.Answer, it.HasAnswer = a, true
	return nil
}

// retitle sets an item's title and keeps references to it right.
func (d *Doc) retitle(it *Item, title string) {
	old := it.Title
	if normTitle(old) == normTitle(title) {
		it.Title = title
		return
	}
	// only a title that names this item alone can be rewritten safely
	if id, ok := d.Resolve(old); ok && id == it.ID {
		it.Title = title
		d.mapRefs(func(inner string) (string, bool) {
			if !strings.HasPrefix(inner, "#") && normTitle(inner) == normTitle(old) {
				return title, true
			}
			return "", false
		})
		return
	}
	it.Title = title
}

func (d *Doc) opRename(op PatchOp) error {
	it, _, err := d.topItem(op.Item)
	if err != nil {
		return err
	}
	if it.Title == "" || (it.Style == StyleBullet && !it.Named) {
		return fmt.Errorf("“%s” has no title to rename", op.Item)
	}
	title := strings.TrimSpace(op.Title)
	if title == "" || oneLine("a title", title) != nil {
		return fmt.Errorf("the new title must be one line of text")
	}
	if it.Style == StyleBullet && strings.Contains(title, "*") {
		return fmt.Errorf("a name written in bold cannot contain *")
	}
	d.retitle(it, title)
	return nil
}

func (d *Doc) opRemove(op PatchOp) error {
	l, ok := d.locate(op.Item)
	if !ok {
		return fmt.Errorf("there is no item “%s”", op.Item)
	}
	switch {
	case l.entry != nil:
		l.list.Entries = append(l.list.Entries[:l.pos:l.pos], l.list.Entries[l.pos+1:]...)
		if len(l.list.Entries) == 0 {
			for i, b := range l.owner.Body {
				if b == Node(l.list) {
					l.owner.Body = removeNode(l.owner.Body, i)
					break
				}
			}
		}
	case l.child != nil:
		l.owner.Body = removeNode(l.owner.Body, l.pos)
	default:
		l.sec.Nodes = removeNode(l.sec.Nodes, l.idx)
	}
	return nil
}

func (d *Doc) opMove(op PatchOp) error {
	l, ok := d.locate(op.Item)
	if !ok {
		return fmt.Errorf("there is no item “%s”", op.Item)
	}
	switch {
	case l.child != nil:
		return fmt.Errorf("a field cannot be moved; remove it and add it again")
	case l.entry != nil:
		return d.moveEntry(l, op)
	}
	it := l.item
	var dest *Section
	if op.Parent == "" {
		dest = l.sec
		if op.After == nil {
			return fmt.Errorf("it does not say where to move it (parent, after)")
		}
	} else {
		var err error
		if dest, err = d.ensureSection(op.Parent); err != nil {
			return err
		}
		if dest.Kind != l.sec.Kind {
			return fmt.Errorf("a %s cannot move from %s to %s", it.Kind, l.sec.Name, dest.Name)
		}
	}
	if op.After != nil && *op.After == op.Item {
		return fmt.Errorf("an item cannot be placed after itself")
	}
	l.sec.Nodes = removeNode(l.sec.Nodes, l.idx)
	at, err := dest.itemPosition(op.After)
	if err != nil {
		return err
	}
	dest.Nodes = insertNode(dest.Nodes, at, it)
	return nil
}

func (d *Doc) moveEntry(l loc, op PatchOp) error {
	target := l.owner
	if op.Parent != "" {
		pl, ok := d.locate(op.Parent)
		if !ok || pl.item == nil {
			return fmt.Errorf("“%s” is not an item an entry can move to", op.Parent)
		}
		target = pl.item
	}
	if op.After == nil && target == l.owner {
		return fmt.Errorf("it does not say where to move it (parent, after)")
	}
	if target.Style != StyleBlock {
		return fmt.Errorf("only an item written as a block can have a list")
	}
	e := l.entry
	// take it out
	l.list.Entries = append(l.list.Entries[:l.pos:l.pos], l.list.Entries[l.pos+1:]...)
	var dst *ListProp
	for _, b := range target.Body {
		if lp, ok := b.(*ListProp); ok && lp.Key == l.list.Key {
			dst = lp
		}
	}
	if dst == nil {
		dst = &ListProp{Key: l.list.Key}
		target.Body = append(target.Body, dst)
	}
	at := len(dst.Entries)
	if op.After != nil {
		at = 0
		if *op.After != "" {
			at = -1
			for i, x := range dst.Entries {
				if x.ID == *op.After {
					at = i + 1
				}
			}
			if at < 0 {
				return fmt.Errorf("“%s” is not in the “%s” list there", *op.After, l.list.Key)
			}
		}
	}
	dst.Entries = append(dst.Entries, nil)
	copy(dst.Entries[at+1:], dst.Entries[at:])
	dst.Entries[at] = e
	if len(l.list.Entries) == 0 {
		for i, b := range l.owner.Body {
			if b == Node(l.list) {
				l.owner.Body = removeNode(l.owner.Body, i)
				break
			}
		}
	}
	return nil
}

func (d *Doc) opSplit(op PatchOp) error {
	_, l, err := d.topItem(op.Item)
	if err != nil {
		return err
	}
	if len(op.Into) == 0 {
		return fmt.Errorf("a split needs the new items to make")
	}
	at := l.idx + 1
	for _, sub := range op.Into {
		if sub.Op != "" && sub.Op != "add" {
			return fmt.Errorf("a split makes new items; each must be an add")
		}
		it, err := d.newItem(l.sec.Kind, sub)
		if err != nil {
			return err
		}
		l.sec.Nodes = insertNode(l.sec.Nodes, at, it)
		at++
	}
	return nil
}

func (d *Doc) opMerge(op PatchOp) error {
	var items []*Item
	var first loc
	for i, id := range op.Items {
		it, l, err := d.topItem(id)
		if err != nil {
			return err
		}
		if i == 0 {
			first = l
		} else if l.sec != first.sec {
			return fmt.Errorf("“%s” and “%s” are in different sections", op.Items[0], id)
		}
		for _, o := range items {
			if o == it {
				return fmt.Errorf("“%s” is listed twice", id)
			}
		}
		items = append(items, it)
	}
	keep := items[0]
	if keep.Style == StyleQuestion {
		return fmt.Errorf("questions are answered, not merged")
	}
	for _, o := range items[1:] {
		if o.Style != keep.Style {
			return fmt.Errorf("“%s” and “%s” are written differently and cannot be merged", keep.ID, o.ID)
		}
	}
	// what each joined item was called, to point references at the survivor
	type gone struct {
		id, title string
		unique    bool
	}
	var gones []gone
	for _, o := range items[1:] {
		id, ok := d.Resolve(o.Title)
		gones = append(gones, gone{id: o.ID, title: o.Title, unique: o.Title != "" && ok && id == o.ID})
	}
	for _, o := range items[1:] {
		if keep.Style == StyleBullet {
			if o.Text != "" {
				if keep.Text != "" {
					keep.Text += " "
				}
				keep.Text += o.Text
			}
			keep.Body = append(keep.Body, o.Body...)
			continue
		}
		for _, b := range o.Body {
			switch b := b.(type) {
			case *Prop:
				var mine *Prop
				for _, kb := range keep.Body {
					if p, ok := kb.(*Prop); ok && p.Key == b.Key {
						mine = p
					}
				}
				if mine == nil {
					keep.Body = append(keep.Body, b)
					continue
				}
				elems := splitList(mine.Value)
				for _, v := range splitList(b.Value) {
					dup := false
					for _, e := range elems {
						if d.sameRef(e, v) {
							dup = true
						}
					}
					if !dup {
						elems = append(elems, v)
					}
				}
				mine.Value = strings.Join(elems, ", ")
			case *ListProp:
				var mine *ListProp
				for _, kb := range keep.Body {
					if lp, ok := kb.(*ListProp); ok && lp.Key == b.Key {
						mine = lp
					}
				}
				if mine == nil {
					keep.Body = append(keep.Body, b)
				} else {
					mine.Entries = append(mine.Entries, b.Entries...)
				}
			default:
				keep.Body = append(keep.Body, b)
			}
		}
	}
	// drop the joined items
	for _, o := range items[1:] {
		for i, n := range first.sec.Nodes {
			if n == Node(o) {
				first.sec.Nodes = removeNode(first.sec.Nodes, i)
				break
			}
		}
	}
	if t := strings.TrimSpace(op.Title); t != "" {
		if oneLine("a title", t) != nil {
			return fmt.Errorf("the new title must be one line of text")
		}
		if keep.Title == "" {
			return fmt.Errorf("“%s” has no title to change", keep.ID)
		}
		d.retitle(keep, t)
	}
	d.mapRefs(func(inner string) (string, bool) {
		for _, g := range gones {
			if inner == "#"+g.id {
				return "#" + keep.ID, true
			}
			if g.unique && normTitle(inner) == normTitle(g.title) {
				return keep.Title, true
			}
		}
		return "", false
	})
	return nil
}

func (d *Doc) opReplace(op PatchOp) error {
	_, l, err := d.topItem(op.Item)
	if err != nil {
		return err
	}
	if op.Snap == nil {
		return fmt.Errorf("a replace needs the item to put back")
	}
	l.sec.Nodes[l.idx] = op.Snap.item()
	return nil
}

func (d *Doc) opRestore(op PatchOp) error {
	if op.Snap == nil || op.Index == nil {
		return fmt.Errorf("a restore needs the item and where it goes")
	}
	sec, err := d.ensureSection(op.Section)
	if err != nil {
		return err
	}
	if *op.Index < 0 || *op.Index > len(sec.Nodes) {
		return fmt.Errorf("position %d is outside %s", *op.Index, sec.Name)
	}
	if op.Snap.ID != "" && d.idTaken(op.Snap.ID) {
		return fmt.Errorf("the id “%s” is already used", op.Snap.ID)
	}
	sec.Nodes = insertNode(sec.Nodes, *op.Index, op.Snap.item())
	return nil
}

func (d *Doc) opPrune(op PatchOp) error {
	for i := len(d.Sections) - 1; i >= 0; i-- {
		if strings.EqualFold(collapse(d.Sections[i].Name), collapse(op.Section)) {
			if len(d.Sections[i].Nodes) > 0 {
				return fmt.Errorf("%s is not empty", d.Sections[i].Name)
			}
			d.Sections = append(d.Sections[:i:i], d.Sections[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("there is no section %s", op.Section)
}

// validPropName reports whether k can be written as a property name: one to
// three lowercase words.
func validPropName(k string) bool {
	got, _, ok := propKey(k + ": x")
	return ok && got == k
}

// opSection makes an empty section (Invert's undo of a prune).
func (d *Doc) opSection(op PatchOp) error {
	if strings.TrimSpace(op.Section) == "" {
		return fmt.Errorf("a section needs a name")
	}
	for _, canon := range sectionNames {
		if strings.EqualFold(canon, collapse(op.Section)) {
			d.Sections = append(d.Sections, &Section{Name: canon, Known: true, Kind: sectionKinds[strings.ToLower(canon)]})
			return nil
		}
	}
	d.Sections = append(d.Sections, &Section{Name: op.Section, Kind: "note"})
	return nil
}

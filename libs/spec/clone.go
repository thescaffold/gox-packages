package spec

// Clone is a deep copy: editing the copy never touches the original.
func (d *Doc) Clone() *Doc {
	c := *d
	c.Settings = append([]Setting(nil), d.Settings...)
	c.Pre = cloneNodes(d.Pre)
	c.Intro = cloneNodes(d.Intro)
	c.Sections = make([]*Section, len(d.Sections))
	for i, s := range d.Sections {
		sc := *s
		sc.Nodes = cloneNodes(s.Nodes)
		c.Sections[i] = &sc
	}
	return &c
}

func cloneNodes(in []Node) []Node {
	if in == nil {
		return nil
	}
	out := make([]Node, len(in))
	for i, n := range in {
		out[i] = cloneNode(n)
	}
	return out
}

func cloneNode(n Node) Node {
	switch n := n.(type) {
	case *Para:
		c := *n
		c.Lines = append([]string(nil), n.Lines...)
		return &c
	case *Raw:
		c := *n
		c.Lines = append([]string(nil), n.Lines...)
		return &c
	case *Comment:
		c := *n
		c.Lines = append([]string(nil), n.Lines...)
		return &c
	case *Prop:
		c := *n
		return &c
	case *ListProp:
		c := *n
		c.Entries = make([]*Entry, len(n.Entries))
		for i, e := range n.Entries {
			ec := *e
			c.Entries[i] = &ec
		}
		return &c
	case *Child:
		c := *n
		c.Cont = append([]string(nil), n.Cont...)
		return &c
	case *Item:
		c := *n
		c.Cont = append([]string(nil), n.Cont...)
		c.Body = cloneNodes(n.Body)
		c.Options = append([]Option(nil), n.Options...)
		return &c
	}
	return n
}

// itemText is the canonical text of one item, which is what two items are
// compared by: equal text means equal content.
func itemText(it *Item) string {
	w := &printer{}
	w.item(it)
	return w.sb.String()
}

// ItemSnap is an item with everything in it, as plain data a patch can carry
// (Invert uses it to put an item back exactly as it was).
type ItemSnap struct {
	Kind      string     `json:"kind"`
	Style     int        `json:"style"`
	Title     string     `json:"title,omitempty"`
	Named     bool       `json:"named,omitempty"`
	Text      string     `json:"text,omitempty"`
	ID        string     `json:"id,omitempty"`
	Cont      []string   `json:"cont,omitempty"`
	Body      []NodeSnap `json:"body,omitempty"`
	Options   []Option   `json:"options,omitempty"`
	Answer    string     `json:"answer,omitempty"`
	HasAnswer bool       `json:"hasAnswer,omitempty"`
}

// NodeSnap is one node of an item's body.
type NodeSnap struct {
	Type    string      `json:"type"` // para, raw, comment, prop, list, child
	Lines   []string    `json:"lines,omitempty"`
	Key     string      `json:"key,omitempty"`
	Value   string      `json:"value,omitempty"`
	Entries []EntrySnap `json:"entries,omitempty"`
	Text    string      `json:"text,omitempty"`
	ID      string      `json:"id,omitempty"`
	Depth   int         `json:"depth,omitempty"`
	Cont    []string    `json:"cont,omitempty"`
}

// EntrySnap is one entry of a list.
type EntrySnap struct {
	Text string `json:"text,omitempty"`
	ID   string `json:"id,omitempty"`
	Box  int    `json:"box,omitempty"`
}

func snapOf(it *Item) ItemSnap {
	s := ItemSnap{Kind: it.Kind, Style: int(it.Style), Title: it.Title, Named: it.Named, Text: it.Text, ID: it.ID,
		Cont: append([]string(nil), it.Cont...), Options: append([]Option(nil), it.Options...), Answer: it.Answer, HasAnswer: it.HasAnswer}
	for _, n := range it.Body {
		switch n := n.(type) {
		case *Para:
			s.Body = append(s.Body, NodeSnap{Type: "para", Lines: append([]string(nil), n.Lines...)})
		case *Raw:
			s.Body = append(s.Body, NodeSnap{Type: "raw", Lines: append([]string(nil), n.Lines...)})
		case *Comment:
			s.Body = append(s.Body, NodeSnap{Type: "comment", Lines: append([]string(nil), n.Lines...)})
		case *Prop:
			s.Body = append(s.Body, NodeSnap{Type: "prop", Key: n.Key, Value: n.Value})
		case *ListProp:
			ns := NodeSnap{Type: "list", Key: n.Key}
			for _, e := range n.Entries {
				ns.Entries = append(ns.Entries, EntrySnap{Text: e.Text, ID: e.ID, Box: int(e.Box)})
			}
			s.Body = append(s.Body, ns)
		case *Child:
			s.Body = append(s.Body, NodeSnap{Type: "child", Text: n.Text, ID: n.ID, Depth: n.Depth, Cont: append([]string(nil), n.Cont...)})
		}
	}
	return s
}

func (s ItemSnap) item() *Item {
	it := &Item{Kind: s.Kind, Style: Style(s.Style), Title: s.Title, Named: s.Named, Text: s.Text, ID: s.ID,
		Cont: append([]string(nil), s.Cont...), Options: append([]Option(nil), s.Options...), Answer: s.Answer, HasAnswer: s.HasAnswer}
	for _, n := range s.Body {
		switch n.Type {
		case "para":
			it.Body = append(it.Body, &Para{Lines: append([]string(nil), n.Lines...)})
		case "raw":
			it.Body = append(it.Body, &Raw{Lines: append([]string(nil), n.Lines...)})
		case "comment":
			it.Body = append(it.Body, &Comment{Lines: append([]string(nil), n.Lines...)})
		case "prop":
			it.Body = append(it.Body, &Prop{Key: n.Key, Value: n.Value})
		case "list":
			lp := &ListProp{Key: n.Key}
			for _, e := range n.Entries {
				lp.Entries = append(lp.Entries, &Entry{Text: e.Text, ID: e.ID, Box: Box(e.Box)})
			}
			it.Body = append(it.Body, lp)
		case "child":
			it.Body = append(it.Body, &Child{Text: n.Text, ID: n.ID, Depth: n.Depth, Cont: append([]string(nil), n.Cont...)})
		}
	}
	return it
}

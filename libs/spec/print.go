package spec

import "strings"

type printer struct {
	sb    strings.Builder
	lines int
	last  string
}

func (w *printer) line(s string) {
	w.sb.WriteString(s)
	w.sb.WriteByte('\n')
	w.lines++
	w.last = s
}

// blank ends the current block with an empty line, never at the very start and
// never twice in a row.
func (w *printer) blank() {
	if w.lines > 0 && w.last != "" {
		w.line("")
	}
}

// Print gives the canonical text of a document. Reading it again and printing
// gives the same text.
func Print(d *Doc) string {
	w := &printer{}
	for _, n := range d.Pre {
		w.node(n)
		w.blank()
	}
	if d.Version != "" {
		w.line("ospec: " + d.Version)
	}
	if d.HasTitle {
		w.line("# " + d.Title)
		if d.Summary != "" {
			w.line("> " + d.Summary)
		}
	}
	if len(d.Settings) > 0 {
		w.blank()
		for _, s := range d.Settings {
			w.line(prop(s.Key, s.Value))
		}
	}
	if len(d.Intro) > 0 {
		w.blank()
		for i, n := range d.Intro {
			if i > 0 {
				w.blank()
			}
			w.node(n)
		}
	}
	for _, s := range d.Sections {
		w.section(s)
	}
	return w.sb.String()
}

func prop(key, value string) string {
	if value == "" {
		return key + ":"
	}
	return key + ": " + value
}

// node prints the free-standing nodes (text, raw, comments).
func (w *printer) node(n Node) {
	switch n := n.(type) {
	case *Para:
		for _, l := range n.Lines {
			w.line(l)
		}
	case *Raw:
		for _, l := range n.Lines {
			w.line(l)
		}
	case *Comment:
		for _, l := range n.Lines {
			w.line(l)
		}
	}
}

func bulletish(n Node) bool {
	it, ok := n.(*Item)
	return ok && it.Style != StyleBlock
}

func (w *printer) section(s *Section) {
	w.blank()
	w.line("## " + s.Name)
	var prev Node
	for _, n := range s.Nodes {
		switch {
		case bulletish(n):
			if prev != nil && !bulletish(prev) {
				w.blank()
			}
			// a bullet right under a question could be read as one of its options
			if q, ok := prev.(*Item); ok && q.Style == StyleQuestion && n.(*Item).Style == StyleBullet {
				w.blank()
			}
		default:
			w.blank()
		}
		switch n := n.(type) {
		case *Item:
			w.item(n)
		default:
			w.node(n)
		}
		prev = n
	}
}

func withID(text, id string) string {
	if id == "" {
		return text
	}
	if text == "" {
		return "{#" + id + "}"
	}
	return text + " {#" + id + "}"
}

func (w *printer) item(it *Item) {
	switch it.Style {
	case StyleQuestion:
		w.line("? " + withID(it.Text, it.ID))
		for _, o := range it.Options {
			if o.Text == "" {
				w.line("- (" + o.Key + ")")
			} else {
				w.line("- (" + o.Key + ") " + o.Text)
			}
		}
		if it.HasAnswer {
			w.line(prop("answer", it.Answer))
		}
	case StyleBullet:
		var head string
		switch {
		case it.Named && it.ID != "":
			head = "**" + it.Title + "** {#" + it.ID + "}"
			if it.Text != "" {
				head += ": " + it.Text
			}
		case it.Named:
			head = "**" + it.Title + "**"
			if it.Text != "" {
				head += ": " + it.Text
			}
		default:
			head = withID(it.Text, it.ID)
		}
		w.line("- " + head)
		for _, c := range it.Cont {
			w.line("  " + c)
		}
		for _, n := range it.Body {
			if c, ok := n.(*Child); ok {
				w.child(c, 2)
			}
		}
	default:
		w.line("### " + withID(it.Title, it.ID))
		w.body(it.Body)
	}
}

func (w *printer) child(c *Child, base int) {
	pad := strings.Repeat(" ", base+2*c.Depth)
	w.line(pad + "- " + withID(c.Text, c.ID))
	for _, l := range c.Cont {
		w.line(pad + "  " + l)
	}
}

func (w *printer) body(nodes []Node) {
	for i, n := range nodes {
		var prev Node
		if i > 0 {
			prev = nodes[i-1]
		}
		switch n := n.(type) {
		case *Para:
			// a blank line keeps text from being read as the end of the bullet above it,
			// and keeps two paragraphs apart
			switch prev.(type) {
			case *Para, *Child:
				w.blank()
			}
			w.node(n)
		case *Raw, *Comment:
			w.node(n)
		case *Prop:
			w.line(prop(n.Key, n.Value))
		case *ListProp:
			w.line(n.Key + ":")
			for _, e := range n.Entries {
				w.line("- " + entry(e))
			}
		case *Child:
			// a bullet right after a bare "key:" or a list would be read as part of it
			switch p := prev.(type) {
			case *ListProp:
				w.blank()
			case *Prop:
				if p.Value == "" {
					w.blank()
				}
			}
			w.child(n, 0)
		}
	}
}

func entry(e *Entry) string {
	text := e.Text
	switch e.Box {
	case BoxOpen:
		text = strings.TrimSpace("[ ] " + text)
	case BoxDone:
		text = strings.TrimSpace("[x] " + text)
	}
	return withID(text, e.ID)
}

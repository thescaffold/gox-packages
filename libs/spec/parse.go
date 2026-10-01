package spec

import (
	"fmt"
	"strings"
)

type parser struct {
	toks  []tok
	i     int
	d     *Doc
	diags []Diagnostic
}

// Parse reads an OSpec document. It never fails: whatever it does not
// understand is kept in the tree as Raw text and reported in the diagnostics.
func Parse(text string) (*Doc, []Diagnostic) {
	text = strings.TrimPrefix(text, "\uFEFF")
	p := &parser{toks: lex(text), d: &Doc{}}
	p.header()
	for p.i < len(p.toks) {
		p.section()
	}
	p.checkVersion()
	return p.d, p.diags
}

func (p *parser) diag(line, col int, sev Severity, code, msg string, fix *Edit) {
	p.diags = append(p.diags, Diagnostic{Line: line, Col: col, Severity: sev, Code: code, Message: msg, Fix: fix})
}

func (p *parser) idProblem(t tok, pr *idProblem) {
	if pr == nil {
		return
	}
	// the marker's column is relative to the text it was cut from; find it in the line
	col := strings.LastIndex(t.raw, "{#") + 1
	if col <= 0 {
		col = 1
	}
	if pr.code == "id-unclosed" {
		p.diag(t.n, col, Warning, pr.code, "This line has an id marker that is not closed. Write it as {#name} or remove it.",
			&Edit{Line: t.n, Col: len(strings.TrimRight(t.raw, " \t")) + 1, EndCol: len(strings.TrimRight(t.raw, " \t")) + 1, Text: "}"})
		return
	}
	p.diag(t.n, col, Warning, pr.code, "This id marker has characters an id cannot have. Ids are letters, numbers, - and _, written {#name}.", nil)
}

func (p *parser) checkVersion() {
	switch p.d.Version {
	case "1":
	case "":
		p.diag(1, 1, Warning, "no-version", "This file does not say which version of the language it uses. Add the line “ospec: 1” at the top.",
			&Edit{Line: 1, Col: 1, EndCol: 1, Text: "ospec: 1\n"})
	default:
		p.diag(1, 1, Warning, "bad-version", fmt.Sprintf("This file says it is version %q of the language; this tool reads version 1, so it is read as version 1 where it can be.", p.d.Version), nil)
	}
	if !p.d.HasTitle {
		p.diag(1, 1, Warning, "no-title", "The document has no title. Start it with a line like “# My system”.", nil)
	}
}

// ---- header ----

func (p *parser) header() {
	var para *Para
	add := func(n Node) {
		para = nil
		if p.d.HasTitle {
			p.d.Intro = append(p.d.Intro, n)
		} else {
			p.d.Pre = append(p.d.Pre, n)
		}
	}
	for p.i < len(p.toks) {
		t := p.toks[p.i]
		switch {
		case t.kind == kSection:
			return
		case t.kind == kBlank:
			para = nil
			p.i++
		case t.kind == kProp:
			para = nil
			if t.key == "ospec" && p.d.Version == "" && t.text != "" {
				p.d.Version = t.text
			} else {
				p.d.Settings = append(p.d.Settings, Setting{Key: t.key, Value: t.text, Line: t.n})
			}
			p.i++
		case t.kind == kTitle && !p.d.HasTitle:
			para = nil
			p.d.Title, p.d.HasTitle = t.text, true
			p.i++
			if p.i < len(p.toks) && p.toks[p.i].kind == kQuote {
				p.d.Summary = p.toks[p.i].text
				p.i++
			}
		case t.kind == kTitle:
			p.diag(t.n, 1, Warning, "second-title", "The document already has a title, so this heading was kept as written, not used as a title.", nil)
			add(&Raw{Lines: []string{t.raw}, Line: t.n})
			p.i++
		case t.kind == kBlock:
			p.diag(t.n, 1, Warning, "outside-section", "This item is not inside a section (“## Features”, “## Data”, …), so it was kept as written.", nil)
			add(&Raw{Lines: []string{t.raw}, Line: t.n})
			p.i++
		case t.kind == kComment:
			add(p.comment())
		case t.kind == kFence:
			add(p.fence())
		default:
			if para == nil {
				np := &Para{Line: t.n}
				add(np)
				para = np
			}
			para.Lines = append(para.Lines, strings.TrimRight(t.raw, " \t"))
			p.i++
		}
	}
}

// comment consumes an HTML comment, which may span lines.
func (p *parser) comment() *Comment {
	t := p.toks[p.i]
	c := &Comment{Line: t.n}
	for p.i < len(p.toks) {
		l := p.toks[p.i]
		c.Lines = append(c.Lines, l.raw)
		p.i++
		probe := l.raw
		if len(c.Lines) == 1 {
			probe = l.raw[len("<!--"):]
		}
		if strings.Contains(probe, "-->") {
			return c
		}
	}
	p.diag(t.n, 1, Warning, "comment-unclosed", "This comment is never closed with -->, so everything after it is part of the comment. It was closed at the end of the file.", nil)
	c.Lines[len(c.Lines)-1] += "-->"
	return c
}

// fence consumes a fenced code block, which is kept exactly as written.
func (p *parser) fence() *Raw {
	t := p.toks[p.i]
	open := strings.TrimSpace(t.raw)
	ch := open[0]
	n := 0
	for n < len(open) && open[n] == ch {
		n++
	}
	r := &Raw{Line: t.n, Lines: []string{t.raw}}
	p.i++
	for p.i < len(p.toks) {
		l := p.toks[p.i]
		r.Lines = append(r.Lines, l.raw)
		p.i++
		s := strings.TrimSpace(l.raw)
		if len(s) >= n && strings.Trim(s, string(ch)) == "" {
			return r
		}
	}
	p.diag(t.n, 1, Warning, "fence-unclosed", "This code block is never closed, so everything after it was kept as written. It was closed at the end of the file.", nil)
	r.Lines = append(r.Lines, strings.Repeat(string(ch), n))
	return r
}

// ---- sections ----

func (p *parser) section() {
	t := p.toks[p.i]
	// only a "## " line can start a section; anything else here is a bug in the caller
	sec := &Section{Name: t.text, Line: t.n, Kind: "note"}
	for _, name := range sectionNames {
		if strings.EqualFold(strings.Join(strings.Fields(t.text), " "), name) {
			sec.Name, sec.Known, sec.Kind = name, true, sectionKinds[strings.ToLower(name)]
			break
		}
	}
	if !sec.Known {
		p.diag(t.n, 1, Info, "unknown-section", fmt.Sprintf("“%s” is not one of the standard sections, so it is kept as a note.", t.text), nil)
	}
	p.d.Sections = append(p.d.Sections, sec)
	p.i++
	noteLike := sec.Kind == "note"
	for p.i < len(p.toks) {
		t := p.toks[p.i]
		switch {
		case t.kind == kSection:
			return
		case t.kind == kBlank:
			p.i++
		case t.kind == kBlock:
			sec.Nodes = append(sec.Nodes, p.blockItem(sec.Kind))
		case t.kind == kBullet && t.indent == 0:
			sec.Nodes = append(sec.Nodes, p.bulletItem(sec.Kind))
		case t.kind == kQuestion:
			sec.Nodes = append(sec.Nodes, p.question())
		case t.kind == kComment:
			sec.Nodes = append(sec.Nodes, p.comment())
		case t.kind == kFence:
			sec.Nodes = append(sec.Nodes, p.fence())
		case t.kind == kTitle:
			p.diag(t.n, 1, Warning, "second-title", "The document already has a title, so this heading was kept as written, not used as a title.", nil)
			sec.Nodes = append(sec.Nodes, &Raw{Lines: []string{t.raw}, Line: t.n})
			p.i++
		default:
			sec.Nodes = append(sec.Nodes, p.stray(noteLike, sec.Name))
		}
	}
}

// stray gathers a run of lines in a section that belong to no item: free text
// in a note section, kept-as-written text anywhere else.
func (p *parser) stray(noteLike bool, section string) Node {
	t := p.toks[p.i]
	var lines []string
	for p.i < len(p.toks) {
		l := p.toks[p.i]
		if !strayLine(l) {
			break
		}
		lines = append(lines, l.raw)
		p.i++
	}
	if noteLike {
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " \t")
		}
		return &Para{Lines: lines, Line: t.n}
	}
	p.diag(t.n, 1, Info, "raw-text", fmt.Sprintf("This text is not part of an item in “%s”, so it was kept as written. Items start with “###”, “- ” or “? ”.", section), nil)
	return &Raw{Lines: lines, Line: t.n}
}

// strayLine reports whether a line, met at the start of a section's content, is
// plain text rather than the start of an item, comment or heading.
func strayLine(l tok) bool {
	switch l.kind {
	case kText, kProp, kQuote, kDeep:
		return true
	case kBullet:
		return l.indent > 0
	}
	return false
}

// ---- items ----

func (p *parser) blockItem(kind string) *Item {
	t := p.toks[p.i]
	p.i++
	title, id, pr := splitTrailingID(t.text)
	p.idProblem(t, pr)
	it := &Item{Kind: kind, Style: StyleBlock, Title: title, ID: id, Line: t.n}
	it.Body = p.body()
	return it
}

// body reads a block item's body up to the next heading.
func (p *parser) body() []Node {
	var nodes []Node
	var para *Para
	var lastChild *Child
	closePara := func() { para = nil }
	for p.i < len(p.toks) {
		t := p.toks[p.i]
		switch {
		case t.kind == kSection || t.kind == kBlock:
			return nodes
		case t.kind == kBlank:
			closePara()
			lastChild = nil
			p.i++
		case t.kind == kTitle:
			closePara()
			lastChild = nil
			p.diag(t.n, 1, Warning, "second-title", "The document already has a title, so this heading was kept as written, not used as a title.", nil)
			nodes = append(nodes, &Raw{Lines: []string{t.raw}, Line: t.n})
			p.i++
		case t.kind == kComment:
			closePara()
			lastChild = nil
			nodes = append(nodes, p.comment())
		case t.kind == kFence:
			closePara()
			lastChild = nil
			nodes = append(nodes, p.fence())
		case t.kind == kProp:
			closePara()
			lastChild = nil
			if t.text == "" && p.i+1 < len(p.toks) && p.toks[p.i+1].kind == kBullet && p.toks[p.i+1].indent == 0 {
				nodes = append(nodes, p.listProp())
				continue
			}
			nodes = append(nodes, &Prop{Key: t.key, Value: t.text, Line: t.n})
			p.i++
		case t.kind == kBullet:
			closePara()
			text, id, pr := splitTrailingID(t.text)
			p.idProblem(t, pr)
			lastChild = &Child{Text: text, ID: id, Depth: t.indent / 2, Line: t.n}
			nodes = append(nodes, lastChild)
			p.i++
		case t.indent >= 2 && lastChild != nil && t.kind == kText:
			lastChild.Cont = append(lastChild.Cont, strings.TrimLeft(t.raw, " \t"))
			p.i++
		default:
			lastChild = nil
			if para == nil {
				para = &Para{Line: t.n}
				nodes = append(nodes, para)
			}
			para.Lines = append(para.Lines, strings.TrimRight(t.raw, " \t"))
			p.i++
		}
	}
	return nodes
}

func (p *parser) listProp() *ListProp {
	t := p.toks[p.i]
	lp := &ListProp{Key: t.key, Line: t.n}
	p.i++
	for p.i < len(p.toks) {
		l := p.toks[p.i]
		if l.kind != kBullet || l.indent != 0 {
			break
		}
		text, id, pr := splitTrailingID(l.text)
		p.idProblem(l, pr)
		e := &Entry{Line: l.n, ID: id}
		switch {
		case text == "[ ]" || strings.HasPrefix(text, "[ ] "):
			e.Box, text = BoxOpen, strings.TrimSpace(text[3:])
		case text == "[x]" || text == "[X]" || strings.HasPrefix(text, "[x] ") || strings.HasPrefix(text, "[X] "):
			e.Box, text = BoxDone, strings.TrimSpace(text[3:])
		}
		e.Text = text
		lp.Entries = append(lp.Entries, e)
		p.i++
	}
	return lp
}

// splitBullet reads "**Name** {#id}: text", "**Name**: text {#id}" and "text {#id}".
func (p *parser) splitBullet(t tok) (name string, named bool, text, id string) {
	s := t.text
	if strings.HasPrefix(s, "**") {
		if end := strings.Index(s[2:], "**"); end >= 0 {
			cand := strings.TrimSpace(s[2 : 2+end])
			tail := strings.TrimSpace(s[2+end+2:])
			if cand != "" && !strings.Contains(cand, "*") {
				rest, tid := tail, ""
				if strings.HasPrefix(rest, "{#") {
					if close := strings.Index(rest, "}"); close > 0 && validID(rest[2:close]) {
						tid, rest = rest[2:close], strings.TrimSpace(rest[close+1:])
					} else {
						rest = "\x00"
					}
				}
				switch {
				case rest == "":
					return cand, true, "", tid
				case strings.HasPrefix(rest, ":"):
					desc := strings.TrimSpace(rest[1:])
					if tid == "" {
						var pr *idProblem
						desc, tid, pr = splitTrailingID(desc)
						p.idProblem(t, pr)
					}
					return cand, true, desc, tid
				}
			}
		}
	}
	rest, tid, pr := splitTrailingID(s)
	p.idProblem(t, pr)
	return "", false, rest, tid
}

func (p *parser) bulletItem(kind string) *Item {
	t := p.toks[p.i]
	p.i++
	name, named, text, id := p.splitBullet(t)
	it := &Item{Kind: kind, Style: StyleBullet, Title: name, Named: named, Text: text, ID: id, Line: t.n}
	var last *Child
	for p.i < len(p.toks) {
		l := p.toks[p.i]
		switch {
		case l.kind == kBullet && l.indent >= 2:
			text, cid, pr := splitTrailingID(l.text)
			p.idProblem(l, pr)
			last = &Child{Text: text, ID: cid, Depth: (l.indent - 2) / 2, Line: l.n}
			it.Body = append(it.Body, last)
			p.i++
		case l.kind == kBlank:
			j := p.i
			for j < len(p.toks) && p.toks[j].kind == kBlank {
				j++
			}
			if j < len(p.toks) && p.toks[j].kind == kBullet && p.toks[j].indent >= 2 {
				p.i = j
				continue
			}
			return it
		case l.kind == kText && l.indent >= 2:
			if last != nil {
				last.Cont = append(last.Cont, strings.TrimLeft(l.raw, " \t"))
			} else {
				it.Cont = append(it.Cont, strings.TrimLeft(l.raw, " \t"))
			}
			p.i++
		default:
			return it
		}
	}
	return it
}

// optionKey reads "(a) text" from a bullet's text.
func optionKey(s string) (key, text string, ok bool) {
	if !strings.HasPrefix(s, "(") {
		return "", "", false
	}
	end := strings.Index(s, ")")
	if end < 2 || end > 4 {
		return "", "", false
	}
	for _, r := range s[1:end] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return "", "", false
		}
	}
	if end+1 < len(s) && s[end+1] != ' ' {
		return "", "", false
	}
	return s[1:end], strings.TrimSpace(s[end+1:]), true
}

func (p *parser) question() *Item {
	t := p.toks[p.i]
	p.i++
	text, id, pr := splitTrailingID(t.text)
	p.idProblem(t, pr)
	q := &Item{Kind: "question", Style: StyleQuestion, Text: text, ID: id, Line: t.n}
	for p.i < len(p.toks) {
		l := p.toks[p.i]
		if l.kind == kBullet {
			if k, txt, ok := optionKey(l.text); ok {
				q.Options = append(q.Options, Option{Key: k, Text: txt, Line: l.n})
				p.i++
				continue
			}
			break
		}
		if l.kind == kProp && l.key == "answer" && !q.HasAnswer {
			q.Answer, q.HasAnswer = l.text, true
			p.i++
			continue
		}
		break
	}
	return q
}

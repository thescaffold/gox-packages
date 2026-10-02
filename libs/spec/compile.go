package spec

import (
	"fmt"
	"sort"
	"strings"

	"github.com/thescaffold/gox-packages/libs/design"
)

// Compile turns a spec into its design graph (TRD §6.5): users become actors,
// features capabilities, data entities, screens interfaces, parts services,
// integrations and environments themselves, and what the items say about each
// other ("for:", "uses:", "needs:", "does:", "owns:", ...) becomes edges. What the
// spec states is taken literally; an item marked "inferred: yes" (or, for a
// one-line item, ending "(inferred by Origine)") was filled in by the Architect, and
// everything compiled from it is marked Inferred.
//
// Every node keeps the id of its spec item. A relation that cannot be drawn (it
// points at nothing, or between things that cannot be related that way) is left out
// and reported, never guessed at.
func Compile(d *Doc) (*design.Graph, []Diagnostic) {
	c := d.Clone()
	Canonicalize(c, nil)
	cc := &compiler{doc: c, g: &design.Graph{Version: design.Version, Title: c.Title, Summary: c.Summary}, seen: map[string]bool{}, res: c.resolver()}
	cc.nodes()
	cc.fields()
	cc.environmentKeys()
	cc.edges()
	cc.deployments()
	for _, p := range cc.g.Validate() {
		cc.diag(0, Error, "design-invalid", p.Message)
	}
	return cc.g, cc.diags
}

type compiler struct {
	doc   *Doc
	g     *design.Graph
	diags []Diagnostic
	seen  map[string]bool // edges already drawn
	items map[string]*Item
	res   *resolver
}

func (c *compiler) diag(line int, sev Severity, code, msg string) {
	c.diags = append(c.diags, Diagnostic{Line: line, Col: 1, Severity: sev, Code: code, Message: msg})
}

// ---- reading an item ----

func inferred(it *Item) bool {
	for _, b := range it.Body {
		if p, ok := b.(*Prop); ok && p.Key == "inferred" {
			switch strings.ToLower(p.Value) {
			case "yes", "true", "y":
				return true
			}
		}
	}
	return strings.HasSuffix(strings.TrimSpace(it.Text), "(inferred by Origine)")
}

func stripInferred(s string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "(inferred by Origine)"))
}

func (it *Item) prop(key string) (string, bool) {
	for _, b := range it.Body {
		if p, ok := b.(*Prop); ok && p.Key == key {
			return p.Value, true
		}
	}
	return "", false
}

func (it *Item) list(key string) []*Entry {
	for _, b := range it.Body {
		if l, ok := b.(*ListProp); ok && l.Key == key {
			return l.Entries
		}
	}
	return nil
}

// summary is the item's description: the words after a bold name, or the first paragraph.
func (it *Item) summary() string {
	if it.Style == StyleBullet {
		return stripInferred(it.Text)
	}
	for _, b := range it.Body {
		if p, ok := b.(*Para); ok {
			return strings.Join(strings.Fields(strings.Join(p.Lines, " ")), " ")
		}
	}
	return ""
}

func criteria(es []*Entry) []design.Criterion {
	var out []design.Criterion
	for _, e := range es {
		out = append(out, design.Criterion{ID: e.ID, Text: e.Text})
	}
	return out
}

// keysIn reads "needs keys: A, B" out of a line of text; it returns the text
// before it and the keys.
func keysIn(s string) (before string, keys []string) {
	i := indexFold(s, "needs keys:")
	if i < 0 {
		return strings.TrimSpace(s), nil
	}
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s[:i]), ".;,")), keyList(s[i+len("needs keys:"):])
}

// indexFold finds an ASCII word in s ignoring case, by byte position in s itself
// (lowercasing s first would change the length of text that is not valid UTF-8).
func indexFold(s, word string) int {
	for i := 0; i+len(word) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(word)], word) {
			return i
		}
	}
	return -1
}

func keyList(s string) []string {
	var keys []string
	for _, k := range splitList(s) {
		keys = append(keys, strings.TrimSpace(k))
	}
	return keys
}

func (c *compiler) nodes() {
	c.items = map[string]*Item{}
	for _, s := range c.doc.Sections {
		for _, n := range s.Nodes {
			it, ok := n.(*Item)
			if !ok || it.ID == "" {
				continue
			}
			c.items[it.ID] = it
			switch it.Kind {
			case "user":
				c.add(it, design.Actor, func(n *design.Node) {
					n.Summary = it.summary()
					if v, ok := it.prop("can"); ok {
						n.Actions = splitList(v)
					}
				})
			case "feature":
				c.add(it, design.Capability, func(n *design.Node) {
					n.Summary = it.summary()
					n.Priority = c.oneOf(it, "priority", "must", "should", "could")
					n.Rules, n.Criteria, n.Deferred = criteria(it.list("must")), criteria(it.list("done when")), criteria(it.list("not now"))
				})
			case "entity":
				c.add(it, design.Entity, func(n *design.Node) {
					n.Summary = it.summary() // the fields are read once every node exists, so they can point forward
				})
			case "screen":
				c.add(it, design.Interface, func(n *design.Node) {
					n.Summary = it.summary()
					n.Device = c.oneOf(it, "device", "web", "mobile", "both")
					if v, ok := it.prop("actions"); ok {
						n.Actions = splitList(v)
					}
				})
			case "part":
				kind := design.Service
				if v, ok := it.prop("runs as"); ok {
					switch strings.ToLower(v) {
					case "job":
						kind = design.Job
					case "interface":
						kind = design.Interface
					}
				}
				c.add(it, kind, func(n *design.Node) { n.Summary = it.summary() })
			case "integration":
				c.add(it, design.Integration, func(n *design.Node) {
					text, keys := keysIn(it.summary())
					n.Summary = text
					if v, ok := it.prop("needs keys"); ok {
						keys = append(keys, keyList(v)...)
					}
					n.Keys = uniqueSorted(c.configKeys(it, keys))
					n.Provider, _ = it.prop("provider")
				})
			case "environment":
				c.add(it, design.Environment, func(n *design.Node) {
					n.Summary, _ = it.prop("purpose")
					n.Domain, _ = it.prop("domain")
					switch c.oneOf(it, "protected", "yes", "no") {
					case "yes":
						p := true
						n.Protected = &p
					case "no":
						p := false
						n.Protected = &p
					}
					if v, ok := it.prop("needs keys"); ok {
						n.Keys = uniqueSorted(c.configKeys(it, keyList(v)))
					}
				})
			case "limit":
				kind, text := "", it.Text
				if k, rest, ok := strings.Cut(it.Text, ":"); ok {
					switch strings.TrimSpace(strings.ToLower(k)) {
					case "speed", "security", "privacy", "scale", "cost", "accessibility":
						kind, text = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(rest)
					}
				}
				c.g.Limits = append(c.g.Limits, design.Limit{ID: it.ID, Kind: kind, Text: text})
			}
		}
	}
}

func (c *compiler) add(it *Item, kind design.NodeKind, fill func(*design.Node)) {
	name := it.Title
	if name == "" {
		c.diag(it.Line, Warning, "design-no-name", fmt.Sprintf("This %s has no name, so it was left out of the design.", it.Kind))
		return
	}
	n := design.Node{ID: it.ID, Kind: kind, Name: name, Inferred: inferred(it)}
	fill(&n)
	c.g.Nodes = append(c.g.Nodes, n)
}

// oneOf reads a property that may only say one of a few words. Anything else is
// left out of the design, with a warning, rather than carried into it.
func (c *compiler) oneOf(it *Item, prop string, allowed ...string) string {
	v, ok := it.prop(prop)
	if !ok || strings.TrimSpace(v) == "" {
		return ""
	}
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(v), a) {
			return a
		}
	}
	c.diag(it.Line, Warning, "design-bad-value", fmt.Sprintf("“%s” says “%s: %s”, but %s can only be %s, so it was left out of the design.", it.Title, prop, v, prop, strings.Join(allowed, ", ")))
	return ""
}

// configKeys keeps the names of settings (PAYSTACK_SECRET) and drops anything else:
// a design lists which keys are needed, never a value, so something that is not a
// name stays out of it. The warning does not repeat what was dropped.
func (c *compiler) configKeys(it *Item, raw []string) []string {
	var keys []string
	for _, k := range raw {
		if design.ConfigKey(k) {
			keys = append(keys, k)
			continue
		}
		c.diag(it.Line, Warning, "design-bad-key", fmt.Sprintf("“%s” lists something under “needs keys” that is not the name of a setting (names are capital letters, digits and _, like PAYSTACK_SECRET), so it was left out of the design. Never write a value here.", it.Title))
	}
	return keys
}

func uniqueSorted(in []string) []string {
	m := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !m[s] {
			m[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// field reads "name: type — note", where the type may be "many [[Entity]]" or "one of a, b".
func (c *compiler) field(text string, owner *Item) design.Field {
	name, rest, found := strings.Cut(text, ":")
	f := design.Field{Name: strings.TrimSpace(name), Type: "text"}
	if !found {
		return f
	}
	typ := strings.TrimSpace(rest)
	for _, sep := range []string{" — ", " – ", " -- "} {
		if t, note, ok := strings.Cut(typ, sep); ok {
			typ, f.Note = strings.TrimSpace(t), strings.TrimSpace(note)
			break
		}
	}
	if strings.HasPrefix(strings.ToLower(typ), "many ") {
		f.Many, typ = true, strings.TrimSpace(typ[5:])
	}
	switch {
	case strings.Contains(typ, "[["):
		f.Type = "reference"
		if refs := References(typ); len(refs) > 0 {
			ids := c.resolve(refs[0], owner.Line, owner.Title)
			if len(ids) == 1 {
				if k := c.g.Node(ids[0]).Kind; k == design.Entity || k == design.Actor {
					f.Ref = ids[0]
				} else {
					c.diag(owner.Line, Warning, "design-bad-relation", fmt.Sprintf("“%s” has a field “%s” that points at “%s”, but %s %s is not something a field can point at (an entity or a kind of person is), so that link was left out of the design.",
						owner.Title, f.Name, c.g.Node(ids[0]).Name, article(string(k)), k))
				}
			}
		}
	case strings.HasPrefix(strings.ToLower(typ), "one of"):
		f.Type, f.Options = "one of", splitList(typ[len("one of"):])
	case typ != "":
		f.Type = strings.ToLower(typ)
	}
	return f
}

// fields reads each entity's fields, which may point at entities that come after it.
func (c *compiler) fields() {
	for i := range c.g.Nodes {
		n := &c.g.Nodes[i]
		if n.Kind != design.Entity {
			continue
		}
		it := c.items[n.ID]
		for _, b := range it.Body {
			if ch, ok := b.(*Child); ok {
				n.Fields = append(n.Fields, c.field(ch.Text, it))
			}
		}
	}
}

func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}

// ---- relations ----

// resolve finds the node a reference names, reporting what could not be found.
func (c *compiler) resolve(r Ref, line int, who string) []string {
	ids := c.res.matches(r)
	switch len(ids) {
	case 1:
		if c.g.Node(ids[0]) == nil {
			c.diag(line, Warning, "design-not-a-node", fmt.Sprintf("“%s” points at “%s”, which is a rule, criterion or detail and not a part of the design.", who, r.Inner))
			return nil
		}
		return ids
	case 0:
		c.diag(line, Warning, "design-unresolved-ref", fmt.Sprintf("“%s” mentions “%s”, which does not match anything in the spec, so that link was left out of the design.", who, r.Inner))
	default:
		c.diag(line, Warning, "design-ambiguous-ref", fmt.Sprintf("“%s” mentions “%s”, which could mean more than one item, so that link was left out of the design.", who, r.Inner))
	}
	return nil
}

// targets reads a property value as a list of things it names.
func (c *compiler) targets(it *Item, value string) []string {
	var out []string
	for _, el := range splitList(value) {
		refs := References(el)
		if len(refs) == 0 {
			refs = []Ref{{Inner: el}}
		}
		for _, r := range refs {
			out = append(out, c.resolve(r, it.Line, it.Title)...)
		}
	}
	return out
}

func (c *compiler) edge(source *Item, from, to string, kind design.EdgeKind, note string) {
	if from == to {
		return
	}
	fn, tn := c.g.Node(from), c.g.Node(to)
	if fn == nil || tn == nil {
		return
	}
	if !design.Allowed(kind, fn.Kind, tn.Kind) {
		c.diag(source.Line, Warning, "design-bad-relation", fmt.Sprintf("“%s” says it %s “%s”, but %s %s cannot be related to %s %s that way, so that link was left out of the design.",
			source.Title, note, tn.Name, article(string(fn.Kind)), fn.Kind, article(string(tn.Kind)), tn.Kind))
		return
	}
	key := from + "\x00" + string(kind) + "\x00" + to
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.g.Edges = append(c.g.Edges, design.Edge{From: from, To: to, Kind: kind, Source: source.ID, Note: note, Inferred: inferred(source)})
}

func (c *compiler) edges() {
	for _, s := range c.doc.Sections {
		for _, n := range s.Nodes {
			it, ok := n.(*Item)
			if !ok || it.ID == "" || c.g.Node(it.ID) == nil {
				continue
			}
			node := c.g.Node(it.ID)
			rel := func(prop string, from func(target string) (string, string), kind design.EdgeKind, note string) {
				v, ok := it.prop(prop)
				if !ok {
					return
				}
				for _, t := range c.targets(it, v) {
					a, b := from(t)
					c.edge(it, a, b, kind, note)
				}
			}
			outgoing := func(t string) (string, string) { return it.ID, t }
			incoming := func(t string) (string, string) { return t, it.ID }
			switch node.Kind {
			case design.Capability:
				rel("for", incoming, design.Uses, "is for")
				rel("uses", outgoing, design.Uses, "uses")
				rel("needs", outgoing, design.Uses, "needs")
				rel("shows", outgoing, design.Uses, "shows")
			case design.Interface:
				if it.Kind == "screen" {
					rel("for", incoming, design.Uses, "is for")
					rel("shows", outgoing, design.Uses, "shows")
				}
			case design.Service, design.Job:
				rel("does", outgoing, design.Owns, "does")
				rel("owns", outgoing, design.Owns, "owns")
				rel("talks to", outgoing, design.Calls, "talks to")
			case design.Entity:
				for _, f := range node.Fields {
					if f.Ref != "" {
						c.edge(it, it.ID, f.Ref, design.Uses, "has a field “"+f.Name+"” that points at")
					}
				}
			}
			if node.Kind == design.Interface && it.Kind == "part" {
				rel("does", outgoing, design.Owns, "does")
				rel("talks to", outgoing, design.Calls, "talks to")
			}
		}
	}
}

// environmentKeys declares every key an integration needs in every environment.
func (c *compiler) environmentKeys() {
	var all []string
	for _, n := range c.g.Nodes {
		if n.Kind == design.Integration {
			all = append(all, n.Keys...)
		}
	}
	for i := range c.g.Nodes {
		if c.g.Nodes[i].Kind == design.Environment {
			c.g.Nodes[i].Keys = uniqueSorted(append(c.g.Nodes[i].Keys, all...))
		}
	}
}

// deployments sends every deployable part to every environment (the default; the
// spec can say otherwise later).
func (c *compiler) deployments() {
	for _, e := range c.g.Of(design.Environment) {
		env := c.items[e.ID]
		for _, n := range c.g.Nodes {
			switch n.Kind {
			case design.Service, design.Job, design.Interface:
				c.edge(env, n.ID, e.ID, design.DeploysTo, "runs in")
			}
		}
	}
}

package design

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Problem is something wrong with a graph, in words a person can follow.
type Problem struct {
	// Code is a short stable name ("dangling-edge", "duplicate-node", ...).
	Code    string
	Message string
	Node    string
	Edge    *Edge
}

func (p Problem) String() string { return p.Code + ": " + p.Message }

// allowed says which kinds of node each kind of edge may join. The graph is
// compiled from the spec, so a pair outside this table means a relation was
// written the wrong way round or between the wrong things.
var allowed = map[EdgeKind]map[NodeKind][]NodeKind{
	Uses: {
		Actor:      {Capability, Interface},
		Capability: {Capability, Entity, Integration, Interface, Service},
		Interface:  {Entity, Capability, Integration},
		Entity:     {Entity, Actor},
		Service:    {Entity, Integration, Datastore, Service},
		Job:        {Entity, Integration, Datastore, Service},
	},
	Owns: {
		Service:   {Capability, Entity, Datastore},
		Job:       {Capability, Entity},
		Interface: {Capability},
		Datastore: {Entity},
	},
	Calls: {
		Service:   {Service, Job, Integration},
		Job:       {Service, Integration},
		Interface: {Service, Integration},
	},
	Publishes: {
		Service: {Entity, Capability},
		Job:     {Entity, Capability},
	},
	DeploysTo: {
		Service:   {Environment},
		Job:       {Environment},
		Interface: {Environment},
	},
}

// Allowed reports whether an edge of this kind may join these kinds of node.
func Allowed(edge EdgeKind, from, to NodeKind) bool {
	for _, k := range allowed[edge][from] {
		if k == to {
			return true
		}
	}
	return false
}

func validNodeKind(k NodeKind) bool {
	for _, x := range NodeKinds {
		if x == k {
			return true
		}
	}
	return false
}

func validEdgeKind(k EdgeKind) bool {
	for _, x := range EdgeKinds {
		if x == k {
			return true
		}
	}
	return false
}

// Validate checks that the graph is consistent: ids are unique, every edge joins
// two nodes that exist and may be joined that way, no edge is repeated, every
// reference field points at an entity or a kind of person, and no secret value hides in a key list.
func (g *Graph) Validate() []Problem {
	var out []Problem
	add := func(code, msg, node string, e *Edge) {
		out = append(out, Problem{Code: code, Message: msg, Node: node, Edge: e})
	}

	if g.Version != Version {
		add("version", fmt.Sprintf("the graph is version %d, this library reads version %d", g.Version, Version), "", nil)
	}
	kinds := map[string]NodeKind{}
	for _, n := range g.Nodes {
		switch {
		case n.ID == "":
			add("node-no-id", fmt.Sprintf("a %s called “%s” has no id", n.Kind, n.Name), "", nil)
			continue
		case !validNodeKind(n.Kind):
			add("node-kind", fmt.Sprintf("“%s” has the kind “%s”, which is not a kind of node", n.ID, n.Kind), n.ID, nil)
		}
		if _, dup := kinds[n.ID]; dup {
			add("duplicate-node", fmt.Sprintf("two nodes have the id “%s”", n.ID), n.ID, nil)
		}
		kinds[n.ID] = n.Kind
		if n.Name == "" {
			add("node-no-name", fmt.Sprintf("“%s” has no name", n.ID), n.ID, nil)
		}
		switch n.Priority {
		case "", "must", "should", "could":
		default:
			add("priority", fmt.Sprintf("“%s” has the priority “%s”; it should be must, should or could", n.ID, n.Priority), n.ID, nil)
		}
		switch n.Device {
		case "", "web", "mobile", "both":
		default:
			add("device", fmt.Sprintf("“%s” runs on “%s”; it should be web, mobile or both", n.ID, n.Device), n.ID, nil)
		}
		for _, k := range n.Keys {
			if !configKey(k) {
				add("key", fmt.Sprintf("“%s” lists “%s” as a configuration key; keys are capital letters, digits and _ (never values)", n.ID, k), n.ID, nil)
			}
		}
	}
	// fields that point at entities
	for _, n := range g.Nodes {
		for _, f := range n.Fields {
			if f.Ref == "" {
				continue
			}
			if k, ok := kinds[f.Ref]; !ok {
				add("dangling-field", fmt.Sprintf("the field “%s” of “%s” points at “%s”, which is not in the design", f.Name, n.ID, f.Ref), n.ID, nil)
			} else if k != Entity && k != Actor {
				add("field-target", fmt.Sprintf("the field “%s” of “%s” points at “%s”, which is %s %s, not an entity or a kind of person", f.Name, n.ID, f.Ref, article(string(k)), k), n.ID, nil)
			}
		}
	}
	seen := map[string]bool{}
	for i := range g.Edges {
		e := g.Edges[i]
		ep := &g.Edges[i]
		if !validEdgeKind(e.Kind) {
			add("edge-kind", fmt.Sprintf("the edge from “%s” to “%s” has the kind “%s”, which is not a kind of edge", e.From, e.To, e.Kind), "", ep)
			continue
		}
		fk, fok := kinds[e.From]
		tk, tok := kinds[e.To]
		if !fok || !tok {
			missing := e.From
			if fok {
				missing = e.To
			}
			add("dangling-edge", fmt.Sprintf("the edge “%s %s %s” points at “%s”, which is not in the design", e.From, e.Kind, e.To, missing), "", ep)
			continue
		}
		if !Allowed(e.Kind, fk, tk) {
			add("edge-pair", fmt.Sprintf("%s %s cannot %s %s %s (“%s” → “%s”)", article(string(fk)), fk, verb(e.Kind), article(string(tk)), tk, e.From, e.To), "", ep)
		}
		key := e.From + "\x00" + string(e.Kind) + "\x00" + e.To
		if seen[key] {
			add("duplicate-edge", fmt.Sprintf("“%s %s %s” is there twice", e.From, e.Kind, e.To), "", ep)
		}
		seen[key] = true
	}
	// every capability's criteria and rules need ids, and ids are not shared by two things
	ids := map[string]string{}
	for _, n := range g.Nodes {
		for _, group := range [][]Criterion{n.Rules, n.Criteria, n.Deferred} {
			for _, c := range group {
				if c.ID == "" {
					add("criterion-no-id", fmt.Sprintf("a rule or criterion of “%s” has no id", n.ID), n.ID, nil)
				} else if owner, dup := ids[c.ID]; dup {
					add("criterion-dup", fmt.Sprintf("the id “%s” is used by “%s” and by “%s”", c.ID, owner, n.ID), n.ID, nil)
				} else {
					ids[c.ID] = n.ID
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}

func verb(k EdgeKind) string {
	if k == DeploysTo {
		return "deploy to"
	}
	return string(k)
}

// configKey reports whether s looks like the name of a setting (PAYSTACK_SECRET),
// which is all a key list may hold.
func configKey(s string) bool {
	if s == "" || !(s[0] >= 'A' && s[0] <= 'Z') {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// ConfigKey reports whether s can be a configuration key name.
func ConfigKey(s string) bool { return configKey(s) }

// JSON is the stable text of a graph: nodes in graph order, edges sorted.
func (g *Graph) JSON() ([]byte, error) {
	c := *g
	c.Edges = append([]Edge(nil), g.Edges...)
	sort.SliceStable(c.Edges, func(i, j int) bool {
		a, b := c.Edges[i], c.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.To < b.To
	})
	if c.Nodes == nil {
		c.Nodes = []Node{}
	}
	if c.Edges == nil {
		c.Edges = []Edge{}
	}
	return json.MarshalIndent(c, "", "  ")
}

// Parse reads a graph from JSON. Unknown fields are an error, and the graph is
// checked: a graph that is read is a graph that holds together.
func Parse(b []byte) (*Graph, error) {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var g Graph
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("this is not a design graph: %v", err)
	}
	if problems := g.Validate(); len(problems) > 0 {
		return nil, fmt.Errorf("the design graph does not hold together: %s", problems[0].Message)
	}
	return &g, nil
}

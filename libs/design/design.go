// Package design is the system's design as a graph: typed nodes (who uses it,
// what it does, what it is made of, where it runs) and typed edges between them.
//
// The graph is a view compiled from the spec (spec.Compile), not something edited
// on its own: a change to the design is a change to the spec. Every node keeps the
// id of the spec item it came from, and what the spec left open and the Architect
// filled in is flagged Inferred so a person can promote it into the spec.
// This package knows nothing about the spec language; it defines the graph, its
// JSON form and JSON Schema, and the checks that keep it consistent.
package design

// Version is the version of the graph format.
const Version = 1

// NodeKind is what a node is.
type NodeKind string

const (
	Actor       NodeKind = "actor"       // a kind of person who uses the system
	Capability  NodeKind = "capability"  // something the system does for them (a feature)
	Service     NodeKind = "service"     // a Goose module or bounded context
	Entity      NodeKind = "entity"      // a thing the system keeps data about
	Datastore   NodeKind = "datastore"   // where data lives
	Interface   NodeKind = "interface"   // a screen, or an API
	Integration NodeKind = "integration" // an outside service the system talks to
	Job         NodeKind = "job"         // work that runs on a schedule or on demand
	Environment NodeKind = "environment" // a place the system runs (staging, production)
)

// NodeKinds lists every kind, in the order they are shown.
var NodeKinds = []NodeKind{Actor, Capability, Service, Entity, Datastore, Interface, Integration, Job, Environment}

// EdgeKind is how two nodes relate.
type EdgeKind string

const (
	Uses      EdgeKind = "uses"
	Owns      EdgeKind = "owns"
	Calls     EdgeKind = "calls"
	Publishes EdgeKind = "publishes"
	DeploysTo EdgeKind = "deploys_to"
)

// EdgeKinds lists every kind.
var EdgeKinds = []EdgeKind{Uses, Owns, Calls, Publishes, DeploysTo}

// Graph is a compiled design.
type Graph struct {
	Version int     `json:"version"`
	Title   string  `json:"title,omitempty"`
	Summary string  `json:"summary,omitempty"`
	Nodes   []Node  `json:"nodes"`
	Edges   []Edge  `json:"edges"`
	Limits  []Limit `json:"limits,omitempty"`
}

// Node is one thing in the design.
type Node struct {
	// ID is the id of the spec item the node came from, so it stays the same
	// across revisions and across renames.
	ID      string   `json:"id"`
	Kind    NodeKind `json:"kind"`
	Name    string   `json:"name"`
	Summary string   `json:"summary,omitempty"`
	// Inferred marks a node the spec did not state, which the Architect filled in.
	Inferred bool `json:"inferred,omitempty"`

	// capability
	Priority string      `json:"priority,omitempty"` // must, should or could
	Rules    []Criterion `json:"rules,omitempty"`    // what it must do
	Criteria []Criterion `json:"criteria,omitempty"` // how to tell it works (done when)
	Deferred []Criterion `json:"deferred,omitempty"` // left for later (not now)
	// entity
	Fields []Field `json:"fields,omitempty"`
	// interface
	Device  string   `json:"device,omitempty"` // web, mobile or both
	Actions []string `json:"actions,omitempty"`
	// integration
	Provider string `json:"provider,omitempty"`
	// environment
	Domain    string `json:"domain,omitempty"`
	Protected *bool  `json:"protected,omitempty"`
	// integration and environment: the configuration keys it needs. Never values.
	Keys []string `json:"keys,omitempty"`
	// Ownership maps the node to code: path prefixes, filled in once it is built.
	Ownership []string `json:"ownership,omitempty"`
}

// Criterion is a rule or an acceptance criterion with the id it has in the spec.
type Criterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Field is one field of an entity.
type Field struct {
	Name string `json:"name"`
	// Type is text, number, money, date, time, yes/no, email, phone, file, "one of"
	// or "reference"; anything else is kept as written.
	Type    string   `json:"type"`
	Note    string   `json:"note,omitempty"`
	Options []string `json:"options,omitempty"`
	// Ref is the entity a reference field points to; Many says it points to several.
	Ref  string `json:"ref,omitempty"`
	Many bool   `json:"many,omitempty"`
}

// Edge relates two nodes.
type Edge struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind EdgeKind `json:"kind"`
	// Source is the id of the spec item where the relation was written.
	Source   string `json:"source,omitempty"`
	Note     string `json:"note,omitempty"`
	Inferred bool   `json:"inferred,omitempty"`
}

// Limit is a statement about speed, security, privacy, scale, cost or accessibility.
type Limit struct {
	ID   string `json:"id"`
	Kind string `json:"kind,omitempty"`
	Text string `json:"text"`
}

// Node finds a node by id.
func (g *Graph) Node(id string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

// Of lists the nodes of a kind, in graph order.
func (g *Graph) Of(kind NodeKind) []Node {
	var out []Node
	for _, n := range g.Nodes {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

// From lists the edges that leave a node, optionally only of one kind.
func (g *Graph) From(id string, kinds ...EdgeKind) []Edge {
	return g.edges(func(e Edge) bool { return e.From == id }, kinds)
}

// To lists the edges that arrive at a node, optionally only of one kind.
func (g *Graph) To(id string, kinds ...EdgeKind) []Edge {
	return g.edges(func(e Edge) bool { return e.To == id }, kinds)
}

func (g *Graph) edges(match func(Edge) bool, kinds []EdgeKind) []Edge {
	var out []Edge
	for _, e := range g.Edges {
		if !match(e) {
			continue
		}
		if len(kinds) > 0 {
			ok := false
			for _, k := range kinds {
				ok = ok || e.Kind == k
			}
			if !ok {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

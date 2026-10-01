package design

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func yes() *bool { t := true; return &t }

func sample() *Graph {
	return &Graph{
		Version: Version, Title: "Shop",
		Nodes: []Node{
			{ID: "household", Kind: Actor, Name: "Household"},
			{ID: "orders", Kind: Capability, Name: "Recurring orders", Priority: "must",
				Criteria: []Criterion{{ID: "c-1", Text: "An order appears."}}, Rules: []Criterion{{ID: "c-2", Text: "Repeat weekly."}}},
			{ID: "order", Kind: Entity, Name: "Order", Fields: []Field{{Name: "household", Type: "reference", Ref: "household2"}}},
			{ID: "paystack", Kind: Integration, Name: "Paystack", Keys: []string{"PAYSTACK_SECRET"}},
			{ID: "my-orders", Kind: Interface, Name: "My orders", Device: "both"},
			{ID: "production", Kind: Environment, Name: "production", Protected: yes(), Keys: []string{"PAYSTACK_SECRET"}},
		},
		Edges: []Edge{
			{From: "household", To: "orders", Kind: Uses, Source: "orders"},
			{From: "orders", To: "order", Kind: Uses},
			{From: "my-orders", To: "production", Kind: DeploysTo},
		},
	}
}

func codes(ps []Problem) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return strings.Join(out, ",")
}

func TestAConsistentGraphHasNoProblems(t *testing.T) {
	g := sample()
	g.Nodes[2].Fields[0].Ref = "household" // an actor, not an entity: fixed below
	g.Nodes[2].Fields = nil
	if ps := g.Validate(); len(ps) != 0 {
		t.Fatalf("%v", ps)
	}
}

// referential integrity: everything that points at something must point at something there
func TestReferentialIntegrity(t *testing.T) {
	cases := []struct {
		name string
		edit func(g *Graph)
		want string
	}{
		{"an edge to a node that is not there", func(g *Graph) { g.Edges = append(g.Edges, Edge{From: "orders", To: "ghost", Kind: Uses}) }, "dangling-edge"},
		{"an edge from a node that is not there", func(g *Graph) { g.Edges = append(g.Edges, Edge{From: "ghost", To: "order", Kind: Uses}) }, "dangling-edge"},
		{"a field that points nowhere", func(g *Graph) { g.Nodes[2].Fields = []Field{{Name: "x", Type: "reference", Ref: "ghost"}} }, "dangling-field"},
		{"a field that points at something that is not an entity", func(g *Graph) { g.Nodes[2].Fields = []Field{{Name: "x", Type: "reference", Ref: "paystack"}} }, "field-target"},
		{"an actor is a fine target for a field", func(g *Graph) { g.Nodes[2].Fields = []Field{{Name: "x", Type: "reference", Ref: "household"}} }, "!field-target"},
		{"two nodes with one id", func(g *Graph) { g.Nodes = append(g.Nodes, Node{ID: "order", Kind: Entity, Name: "Again"}) }, "duplicate-node"},
		{"the same edge twice", func(g *Graph) { g.Edges = append(g.Edges, g.Edges[1]) }, "duplicate-edge"},
		{"an edge between the wrong kinds", func(g *Graph) { g.Edges = append(g.Edges, Edge{From: "order", To: "household", Kind: Owns}) }, "edge-pair"},
		{"an actor deployed", func(g *Graph) { g.Edges = append(g.Edges, Edge{From: "household", To: "production", Kind: DeploysTo}) }, "edge-pair"},
		{"a kind of node that does not exist", func(g *Graph) { g.Nodes[0].Kind = "person" }, "node-kind"},
		{"a kind of edge that does not exist", func(g *Graph) { g.Edges[0].Kind = "likes" }, "edge-kind"},
		{"a node with no id", func(g *Graph) { g.Nodes = append(g.Nodes, Node{Kind: Actor, Name: "x"}) }, "node-no-id"},
		{"a node with no name", func(g *Graph) { g.Nodes[0].Name = "" }, "node-no-name"},
		{"a criterion with no id", func(g *Graph) { g.Nodes[1].Criteria[0].ID = "" }, "criterion-no-id"},
		{"a criterion id shared by two things", func(g *Graph) { g.Nodes[1].Rules[0].ID = "c-1" }, "criterion-dup"},
		{"a value where a key belongs", func(g *Graph) { g.Nodes[3].Keys = []string{"sk-live-abc"} }, "key"},
		{"a priority that is not one", func(g *Graph) { g.Nodes[1].Priority = "urgent" }, "priority"},
		{"a device that is not one", func(g *Graph) { g.Nodes[4].Device = "watch" }, "device"},
		{"a version this library does not read", func(g *Graph) { g.Version = 9 }, "version"},
	}
	for _, c := range cases {
		g := sample()
		g.Nodes[2].Fields = nil
		c.edit(g)
		got := codes(g.Validate())
		if strings.HasPrefix(c.want, "!") {
			if strings.Contains(got, c.want[1:]) {
				t.Errorf("%s: did not want %s, got %q", c.name, c.want[1:], got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: wanted %s, got %q", c.name, c.want, got)
		}
	}
}

func TestProblemsAreInPlainWords(t *testing.T) {
	g := sample()
	g.Nodes[2].Fields = nil
	g.Edges = append(g.Edges, Edge{From: "orders", To: "ghost", Kind: Uses})
	p := g.Validate()[0]
	if !strings.Contains(p.Message, "“ghost”") || !strings.Contains(p.Message, "not in the design") || p.Edge == nil {
		t.Fatalf("%+v", p)
	}
}

func TestAllowedEdges(t *testing.T) {
	yesPairs := [][3]any{{Uses, Actor, Capability}, {Uses, Capability, Entity}, {Owns, Service, Entity}, {Calls, Service, Integration}, {DeploysTo, Interface, Environment}, {Owns, Datastore, Entity}, {Uses, Entity, Actor}}
	for _, p := range yesPairs {
		if !Allowed(p[0].(EdgeKind), p[1].(NodeKind), p[2].(NodeKind)) {
			t.Errorf("%v should be allowed", p)
		}
	}
	noPairs := [][3]any{{Uses, Actor, Entity}, {Owns, Actor, Capability}, {DeploysTo, Entity, Environment}, {Calls, Entity, Service}, {DeploysTo, Service, Service}}
	for _, p := range noPairs {
		if Allowed(p[0].(EdgeKind), p[1].(NodeKind), p[2].(NodeKind)) {
			t.Errorf("%v should not be allowed", p)
		}
	}
}

func TestJSONRoundTripsAndIsStable(t *testing.T) {
	g := sample()
	g.Nodes[2].Fields = nil
	a, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(a)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := back.JSON(); string(again) != string(a) {
		t.Fatalf("the graph changed on the way through JSON:\n%s\n%s", a, again)
	}
	if len(back.Nodes) != len(g.Nodes) || len(back.Edges) != len(g.Edges) || !reflect.DeepEqual(back.Nodes, g.Nodes) {
		t.Fatalf("nodes changed on the way through JSON")
	}
	// edges come out sorted whatever order they went in
	g.Edges[0], g.Edges[2] = g.Edges[2], g.Edges[0]
	b, _ := g.JSON()
	if string(a) != string(b) {
		t.Fatalf("the JSON depends on edge order:\n%s\n%s", a, b)
	}
	if empty, _ := (&Graph{Version: Version}).JSON(); !strings.Contains(string(empty), `"nodes": []`) || !strings.Contains(string(empty), `"edges": []`) {
		t.Fatalf("an empty graph should say so: %s", empty)
	}
}

func TestParseRefusesWhatIsNotAGraph(t *testing.T) {
	for name, in := range map[string]string{
		"not json":           `{`,
		"an unknown field":   `{"version":1,"nodes":[],"edges":[],"colour":"red"}`,
		"a dangling edge":    `{"version":1,"nodes":[],"edges":[{"from":"a","to":"b","kind":"uses"}]}`,
		"the wrong version":  `{"version":2,"nodes":[],"edges":[]}`,
		"a wrong value type": `{"version":1,"nodes":[{"id":1}],"edges":[]}`,
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestSchemaDescribesTheTypes(t *testing.T) {
	var s map[string]any
	if err := json.Unmarshal(Schema(), &s); err != nil {
		t.Fatal(err)
	}
	if s["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("%v", s["$schema"])
	}
	defs := s["$defs"].(map[string]any)
	node := defs["Node"].(map[string]any)["properties"].(map[string]any)
	// every JSON field of the Go types is in the schema, with no extras
	for _, typ := range []reflect.Type{reflect.TypeOf(Node{}), reflect.TypeOf(Edge{}), reflect.TypeOf(Field{}), reflect.TypeOf(Criterion{}), reflect.TypeOf(Limit{})} {
		props := defs[typ.Name()].(map[string]any)["properties"].(map[string]any)
		n := 0
		for i := 0; i < typ.NumField(); i++ {
			name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			if _, ok := props[name]; !ok {
				t.Errorf("%s.%s is not in the schema", typ.Name(), name)
			}
			n++
		}
		if len(props) != n {
			t.Errorf("%s: schema has %d properties, type has %d fields", typ.Name(), len(props), n)
		}
	}
	// the kinds are enums, and a field with omitempty is not required
	kind := node["kind"].(map[string]any)["enum"].([]any)
	if len(kind) != len(NodeKinds) || kind[0] != "actor" {
		t.Fatalf("node kinds: %v", kind)
	}
	if edgeKinds := defs["Edge"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)["enum"].([]any); len(edgeKinds) != len(EdgeKinds) {
		t.Fatalf("edge kinds: %v", edgeKinds)
	}
	req := defs["Node"].(map[string]any)["required"].([]any)
	if len(req) != 3 { // id, kind, name
		t.Fatalf("required: %v", req)
	}
	if string(Schema()) != string(Schema()) {
		t.Fatal("the schema is not stable")
	}
}

func TestLookups(t *testing.T) {
	g := sample()
	if g.Node("orders") == nil || g.Node("nope") != nil {
		t.Fatal("Node")
	}
	if len(g.Of(Actor)) != 1 || len(g.Of(Capability)) != 1 {
		t.Fatal("Of")
	}
	if len(g.From("orders")) != 1 || len(g.To("orders", Uses)) != 1 || len(g.To("orders", Owns)) != 0 {
		t.Fatal("From/To")
	}
}

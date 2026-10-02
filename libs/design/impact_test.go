package design

import (
	"encoding/json"
	"strings"
	"testing"
)

// a small system: a person uses a capability that uses data and a payment
// service, shown on a screen, deployed to two environments
func shop() *Graph {
	return &Graph{
		Version: Version,
		Nodes: []Node{
			{ID: "person", Kind: Actor, Name: "Person"},
			{ID: "buy", Kind: Capability, Name: "Buy", Priority: "must", Criteria: []Criterion{{ID: "c-1", Text: "A purchase is recorded."}, {ID: "c-2", Text: "A receipt is sent."}}},
			{ID: "browse", Kind: Capability, Name: "Browse", Criteria: []Criterion{{ID: "c-3", Text: "Items are listed."}}},
			{ID: "order", Kind: Entity, Name: "Order", Fields: []Field{{Name: "total", Type: "money"}}},
			{ID: "item", Kind: Entity, Name: "Item", Fields: []Field{{Name: "name", Type: "text"}}},
			{ID: "pay", Kind: Integration, Name: "Pay"},
			{ID: "screen", Kind: Interface, Name: "Checkout", Device: "web"},
			{ID: "staging", Kind: Environment, Name: "staging"},
			{ID: "live", Kind: Environment, Name: "live"},
		},
		Edges: []Edge{
			{From: "person", To: "buy", Kind: Uses},
			{From: "person", To: "browse", Kind: Uses},
			{From: "buy", To: "order", Kind: Uses},
			{From: "buy", To: "pay", Kind: Uses},
			{From: "browse", To: "item", Kind: Uses},
			{From: "screen", To: "order", Kind: Uses},
			{From: "order", To: "item", Kind: Uses},
			{From: "screen", To: "staging", Kind: DeploysTo},
			{From: "screen", To: "live", Kind: DeploysTo},
		},
	}
}

func clone(g *Graph) *Graph {
	b, _ := json.Marshal(g)
	var c Graph
	_ = json.Unmarshal(b, &c)
	return &c
}

func ids(s ImpactSet, k NodeKind) string { return strings.Join(s.IDs(k), ",") }

func TestNothingChangedMeansNothingAffected(t *testing.T) {
	g := shop()
	s := Impact(g, clone(g))
	if !s.Empty() || len(s.Work) != 0 || len(s.Tests) != 0 || len(s.Artifacts) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestCompareSeesNodesAndEdgesButNotOwnershipOrInferredMarks(t *testing.T) {
	a, b := shop(), shop()
	b.Nodes[3].Ownership = []string{"server/orders/"}
	b.Nodes[3].Inferred = true
	if d := Compare(a, b); !d.Empty() {
		t.Fatalf("ownership and inferred marks are not design changes: %+v", d)
	}
	b.Nodes[3].Fields = append(b.Nodes[3].Fields, Field{Name: "paid", Type: "yes/no"})
	b.Nodes[1].Priority = "should"
	b.Nodes[1].Name = "Purchase"
	b.Nodes = append(b.Nodes, Node{ID: "gift", Kind: Capability, Name: "Gift"})
	b.Nodes = b.Nodes[: len(b.Nodes)-2 : len(b.Nodes)-2] // drop environment live (index 8) and gift... rebuild below
	b = clone(b)
	b.Nodes = append(b.Nodes, Node{ID: "gift", Kind: Capability, Name: "Gift"})
	b.Edges = append(b.Edges[:2:2], b.Edges[3:]...) // drop person→browse? index 1 stays; drop buy→order
	d := Compare(a, b)
	got := map[string]string{}
	for _, c := range d.Nodes {
		got[c.ID] = c.Op + ":" + strings.Join(c.Fields, "+")
	}
	if got["order"] != "changed:fields" || got["buy"] != "changed:name+priority" || got["gift"] != "added:" || got["live"] != "removed:" {
		t.Fatalf("%v", got)
	}
	if len(d.Edges) == 0 {
		t.Fatal("the dropped edges are not seen")
	}
}

func TestAChangeReachesWhatDependsOnItAndNothingElse(t *testing.T) {
	before := shop()
	after := clone(before)
	after.Nodes[3].Fields = append(after.Nodes[3].Fields, Field{Name: "paid", Type: "yes/no"}) // Order gains a field
	s := Impact(before, after)

	if got := ids(s, Entity); got != "order" {
		t.Errorf("entities: %s", got)
	}
	// Buy uses Order, the screen shows it: both are affected, not directly
	if got := ids(s, Capability); got != "buy" {
		t.Errorf("capabilities: %s (Browse uses Item, which did not change)", got)
	}
	if got := ids(s, Interface); got != "screen" {
		t.Errorf("screens: %s", got)
	}
	// what Order depends on (Item) and what depends on Item only (Browse) are untouched, as are people
	for _, id := range []string{"item", "browse", "person", "pay"} {
		for _, a := range s.Affected {
			if a.ID == id {
				t.Errorf("%s should not be affected", id)
			}
		}
	}
	// the screen is deployed to both environments, so both need deploying again
	if got := ids(s, Environment); got != "staging,live" {
		t.Errorf("environments: %s", got)
	}
	by := map[string]Affected{}
	for _, a := range s.Affected {
		by[a.ID] = a
	}
	if !by["order"].Direct || by["buy"].Direct || !strings.Contains(by["buy"].Why, "“Order”") {
		t.Errorf("buy: %+v / order: %+v", by["buy"], by["order"])
	}
	// the tests of Buy are re-run, those of Browse are not
	for _, tr := range s.Tests {
		if tr.Capability != "buy" || tr.Status != "rerun" {
			t.Errorf("unexpected test %+v", tr)
		}
	}
	if len(s.Tests) != 2 {
		t.Errorf("%d tests", len(s.Tests))
	}
	if got := strings.Join(s.Artifacts, ","); got != "design.json,design.mmd,erd.mmd,prototype,PRD.md,TRD.md" {
		t.Errorf("artifacts: %s", got)
	}
}

func TestEnvironmentsAndPeopleAreEndsOfTheLine(t *testing.T) {
	before := shop()
	after := clone(before)
	after.Nodes[8].Keys = []string{"LIVE_KEY"} // live's settings changed
	s := Impact(before, after)
	if got := ids(s, Environment); got != "live" || len(s.Affected) != 1 {
		t.Fatalf("a change to an environment must not spread to what runs in it: %+v", s.Affected)
	}
	after = clone(before)
	after.Nodes[0].Name = "Customer" // a person is renamed
	s = Impact(before, after)
	if len(s.Affected) != 1 || s.Affected[0].ID != "person" {
		t.Fatalf("%+v", s.Affected)
	}
}

func TestCriteriaAreNewChangedRemovedOrRerun(t *testing.T) {
	before := shop()
	after := clone(before)
	after.Nodes[1].Criteria[0].Text = "A purchase is saved."                                                       // changed
	after.Nodes[1].Criteria = append(after.Nodes[1].Criteria[:1:1], Criterion{ID: "c-9", Text: "A refund works."}) // c-2 removed, c-9 new
	after.Nodes[2].Criteria = append(after.Nodes[2].Criteria, Criterion{ID: "c-10", Text: "Search works."})        // new on an unaffected capability
	s := Impact(before, after)
	got := map[string]string{}
	for _, tr := range s.Tests {
		got[tr.Criterion] = tr.Capability + ":" + tr.Status
	}
	want := map[string]string{"c-1": "buy:changed", "c-9": "buy:new", "c-2": "buy:removed", "c-10": "browse:new", "c-3": "browse:rerun"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s, want %s (%v)", k, got[k], v, got)
		}
	}
	// a capability that gains a criterion has changed, so all its criteria are re-run
}

func TestCodeFollowsOwnershipBeforeAndAfter(t *testing.T) {
	before := shop()
	before.Nodes[3].Ownership = []string{"server/orders/", "server/db/orders.sql"}
	before.Nodes[1].Ownership = []string{"server/buy/"}
	before.Nodes[6].Ownership = []string{"web/checkout/"}
	after := clone(before)
	after.Nodes[3].Fields = append(after.Nodes[3].Fields, Field{Name: "paid", Type: "yes/no"})
	after.Nodes[1].Ownership = []string{"server/purchases/"} // moved on disk
	s := Impact(before, after)
	got := map[string]string{}
	for _, c := range s.Code {
		got[c.Node] = strings.Join(c.Paths, " ")
	}
	if got["order"] != "server/db/orders.sql server/orders/" || got["buy"] != "server/buy/ server/purchases/" || got["screen"] != "web/checkout/" {
		t.Fatalf("%v", got)
	}
	if len(s.Unbuilt) != 0 {
		t.Fatalf("everything affected has code (environments do not): %v", s.Unbuilt)
	}
	before2 := shop() // nothing built yet
	after2 := clone(before2)
	after2.Nodes[3].Fields = nil
	s = Impact(before2, after2)
	if strings.Join(s.Unbuilt, ",") != "buy,order,screen" || len(s.Code) != 0 {
		t.Fatalf("unbuilt: %v code: %v", s.Unbuilt, s.Code)
	}
}

func TestTheWorkIsWrittenInPlainWords(t *testing.T) {
	before := shop()
	before.Nodes = append(before.Nodes, Node{ID: "old-svc", Kind: Service, Name: "Old"})
	before.Edges = append(before.Edges, Edge{From: "old-svc", To: "order", Kind: Owns})
	after := clone(before)
	// a new service takes Order from the old one, Buy now calls it and not the payment integration
	after.Nodes = append(after.Nodes, Node{ID: "new-svc", Kind: Service, Name: "Orders"})
	after.Edges = []Edge{}
	for _, e := range before.Edges {
		if e.From == "old-svc" || (e.From == "buy" && e.To == "pay") {
			continue
		}
		after.Edges = append(after.Edges, e)
	}
	after.Edges = append(after.Edges,
		Edge{From: "new-svc", To: "order", Kind: Owns},
		Edge{From: "buy", To: "new-svc", Kind: Uses},
		Edge{From: "new-svc", To: "staging", Kind: DeploysTo},
		Edge{From: "new-svc", To: "live", Kind: DeploysTo},
	)
	s := Impact(before, after)
	var titles []string
	for _, w := range s.Work {
		titles = append(titles, w.Kind+": "+w.Title)
	}
	got := strings.Join(titles, "\n")
	for _, want := range []string{
		"add: Create the “Orders” service",
		"move: Move “Order” from “Old” to “Orders”",
		"disconnect: Disconnect “Buy” from “Pay”",
		"connect: Connect “Buy” to “Orders”",
		"deploy: Deploy “Orders” to staging and live",
		"recheck: Re-check “Checkout”",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	// nothing is said twice, and nobody is told to connect a person to something
	seen := map[string]bool{}
	for _, w := range s.Work {
		if seen[w.Title] {
			t.Errorf("%q is there twice", w.Title)
		}
		seen[w.Title] = true
		if w.Why == "" {
			t.Errorf("%q has no reason", w.Title)
		}
	}
	// removing a node: its edges are not separate work
	gone := clone(before)
	gone.Nodes = gone.Nodes[:len(gone.Nodes)-1]
	gone.Edges = gone.Edges[:len(gone.Edges)-1]
	s = Impact(before, gone)
	var gotTitles []string
	for _, w := range s.Work {
		gotTitles = append(gotTitles, w.Title)
	}
	joined := strings.Join(gotTitles, "|")
	if !strings.HasPrefix(joined, "Remove “Old”|Find a new owner for “Order” (it belonged to “Old”)|") || strings.Contains(joined, "Connect") || strings.Contains(joined, "Disconnect") {
		t.Fatalf("%s", joined)
	}
}

func TestImpactJSONIsStableAndNeverNull(t *testing.T) {
	g := shop()
	b, _ := Impact(g, clone(g)).JSON()
	for _, want := range []string{`"affected": []`, `"tests": []`, `"work": []`, `"artifacts": []`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}

func TestRewordingADescriptionChangesNothing(t *testing.T) {
	a, b := shop(), shop()
	b.Nodes[1].Summary = "Now with different words."
	if s := Impact(a, b); !s.Empty() {
		t.Fatalf("%+v", s)
	}
}

func TestWhatOwnsAThingChangesWhenItDoes(t *testing.T) {
	before := shop()
	before.Nodes = append(before.Nodes, Node{ID: "svc", Kind: Service, Name: "Shop service"})
	before.Edges = append(before.Edges, Edge{From: "svc", To: "order", Kind: Owns}, Edge{From: "svc", To: "buy", Kind: Owns})
	after := clone(before)
	after.Nodes[3].Fields = append(after.Nodes[3].Fields, Field{Name: "paid", Type: "yes/no"}) // Order changes
	s := Impact(before, after)
	got := map[string]bool{}
	for _, a := range s.Affected {
		got[a.ID] = true
	}
	// the service that keeps Order changes with it; a feature it also owns is affected only because it uses Order
	if !got["svc"] {
		t.Fatalf("the owner of a changed thing is affected: %v", got)
	}
	// and the other way: the service changing does not make everything it owns stale
	after = clone(before)
	after.Nodes[len(after.Nodes)-1].Name = "Shop backend"
	s = Impact(before, after)
	if len(s.Affected) != 1 || s.Affected[0].ID != "svc" {
		t.Fatalf("a service's rename must not spread to what it owns: %+v", s.Affected)
	}
}

func TestPeopleAreNotAffectedByTheirLinksChanging(t *testing.T) {
	before := shop()
	after := clone(before)
	after.Edges = append(after.Edges, Edge{From: "person", To: "screen", Kind: Uses}) // a person can now use the screen
	s := Impact(before, after)
	for _, a := range s.Affected {
		if a.ID == "person" {
			t.Fatalf("a person is not out of date because they can reach one more thing: %+v", s.Affected)
		}
	}
	if got := ids(s, Interface); got != "screen" {
		t.Fatalf("the screen is: %s", got)
	}
	for _, w := range s.Work {
		if w.Kind == "connect" && w.Node == "person" {
			t.Fatalf("no work to connect a person: %+v", w)
		}
	}
}

func TestANewThingIsBuiltNotMoved(t *testing.T) {
	before := shop()
	before.Nodes = append(before.Nodes, Node{ID: "svc", Kind: Service, Name: "Shop service"})
	after := clone(before)
	after.Nodes = append(after.Nodes, Node{ID: "gift", Kind: Capability, Name: "Gift"})
	after.Edges = append(after.Edges, Edge{From: "svc", To: "gift", Kind: Owns})
	s := Impact(before, after)
	for _, w := range s.Work {
		if w.Kind == "move" {
			t.Fatalf("a new feature is built where it belongs, not moved there: %+v", w)
		}
	}
	if len(s.Work) == 0 || s.Work[0].Title != "Build “Gift”" {
		t.Fatalf("%+v", s.Work)
	}
}

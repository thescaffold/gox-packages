package spec

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/thescaffold/gox-packages/libs/design"
)

func splitOrders(t *testing.T) (before, after *Doc) {
	t.Helper()
	before = grocery(t)
	p, err := ParsePatch([]byte(read(t, "testdata/patches/orders-service.json")))
	if err != nil {
		t.Fatal(err)
	}
	after, err = Apply(before, p)
	if err != nil {
		t.Fatal(err)
	}
	return before, after
}

func titles(s design.ImpactSet) []string {
	var out []string
	for _, w := range s.Work {
		out = append(out, w.Title)
	}
	return out
}

// PLAN M2-02: "Split orders into a separate service" (Appendix E.5) applied to the
// reference example (E.6). The impact set is the fixture's expected capabilities,
// tests and work.
func TestSplittingOrdersHasTheExpectedImpact(t *testing.T) {
	before, after := splitOrders(t)
	s, diags := Impact(before, after, nil)
	if len(diags) != 0 {
		t.Fatalf("%v", diags)
	}

	// what is affected, and which of it changed directly
	got := map[string]string{}
	for _, a := range s.Affected {
		d := "indirect"
		if a.Direct {
			d = "direct"
		}
		got[a.ID] = string(a.Kind) + "/" + d
	}
	want := map[string]string{
		"orders-service":   "service/direct",
		"recurring-orders": "capability/direct",
		"order":            "entity/direct",
		"order-line":       "entity/direct",
		"my-orders":        "interface/indirect", // it shows Order, which moved
		"staging":          "environment/direct",
		"production":       "environment/direct",
	}
	if len(got) != len(want) {
		t.Errorf("affected:\n got %v\nwant %v", got, want)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %q, want %q", id, got[id], w)
		}
	}
	// what is not: the other capability, Product, the integration, the people
	for _, id := range []string{"local-payments", "product", "paystack", "household", "store-admin"} {
		if _, ok := got[id]; ok {
			t.Errorf("%s should not be affected", id)
		}
	}

	// the capability that changed has its acceptance criteria re-run, and no others do
	var tests []string
	for _, tr := range s.Tests {
		tests = append(tests, tr.Capability+"/"+tr.Criterion+"/"+tr.Status)
	}
	if got := strings.Join(tests, ","); got != "recurring-orders/c-3f2a/rerun,recurring-orders/c-b310/rerun,recurring-orders/c-52c9/rerun" {
		t.Errorf("tests: %s", got)
	}

	// the plan, in plain words
	wantWork := []string{
		"Create the “Orders” service",
		"Move “Recurring orders” into “Orders”",
		"Move “Order” into “Orders”",
		"Move “Order line” into “Orders”",
		"Disconnect “Recurring orders” from “Order”",
		"Connect “Recurring orders” to “Orders”",
		"Deploy “Orders” to staging and production",
		"Re-check “My orders”",
	}
	if got := titles(s); strings.Join(got, "\n") != strings.Join(wantWork, "\n") {
		t.Errorf("work:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantWork, "\n"))
	}

	// nothing is built yet, so every real part is unbuilt and no code is named
	if strings.Join(s.Unbuilt, ",") != "recurring-orders,order,order-line,my-orders,orders-service" {
		t.Errorf("unbuilt: %v", s.Unbuilt)
	}
	if len(s.Code) != 0 {
		t.Errorf("code: %v", s.Code)
	}
	// the entity diagram does not change (no data was reshaped); the API and prototype do
	if got := strings.Join(s.Artifacts, ","); got != "design.json,design.mmd,openapi.yaml,prototype,PRD.md,TRD.md" {
		t.Errorf("artifacts: %s", got)
	}

	b, err := s.JSON()
	if err != nil {
		t.Fatal(err)
	}
	golden := "testdata/impact/split-orders.json"
	if *update {
		if err := os.WriteFile(golden, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if want := read(t, golden); string(b)+"\n" != want {
		t.Errorf("the impact set differs from %s:\n%s", golden, b)
	}
	var back map[string]any
	if json.Unmarshal(b, &back) != nil {
		t.Error("the impact JSON is not JSON")
	}
}

// once things are built, the code that belongs to what is affected is named, and
// the impact follows the code: what moved on disk before and after is both listed
func TestSplittingOrdersNamesTheCodeOnceItIsBuilt(t *testing.T) {
	before, after := splitOrders(t)
	s, _ := Impact(before, after, map[string][]string{
		"recurring-orders": {"server/recurring/"},
		"order":            {"server/orders/order.go", "server/db/orders.sql"},
		"order-line":       {"server/orders/line.go"},
		"my-orders":        {"web/src/orders/"},
		"local-payments":   {"server/payments/"},
	})
	code := map[string]string{}
	for _, c := range s.Code {
		code[c.Node] = strings.Join(c.Paths, " ")
	}
	if code["order"] != "server/db/orders.sql server/orders/order.go" || code["my-orders"] != "web/src/orders/" || code["recurring-orders"] != "server/recurring/" {
		t.Errorf("code: %v", code)
	}
	if _, ok := code["local-payments"]; ok {
		t.Error("payments code is not affected")
	}
	// only the new service has nothing yet
	if strings.Join(s.Unbuilt, ",") != "orders-service" {
		t.Errorf("unbuilt: %v", s.Unbuilt)
	}
}

func TestOtherChangesToTheReferenceExample(t *testing.T) {
	d := grocery(t)
	cases := []struct {
		name  string
		ops   []PatchOp
		want  string // affected ids, in order
		tests string
	}{
		{"wording only", []PatchOp{{Op: "text", Item: "local-payments", Text: "Pay by card or transfer."}}, "", ""},
		{"a rule changes", []PatchOp{{Op: "text", Item: "c-91ab", Text: "An item can repeat every 1 to 12 weeks."}}, "recurring-orders", "c-3f2a,c-b310,c-52c9"},
		{"a field is added to Product", []PatchOp{{Op: "add", Parent: "product", List: "fields", Text: "stock: number"}}, "recurring-orders,order-line,product,order,my-orders",
			""},
		{"a new criterion", []PatchOp{{Op: "add", Parent: "local-payments", List: "done when", Text: "A refund works.", ID: "c-refund"}}, "local-payments", "c-1e08,c-refund"},
		{"the integration changes", []PatchOp{{Op: "set", Item: "local-payments", Prop: "needs", Value: &Strings{"[[Paystack]]", "[[Order]]"}}}, "local-payments,order", ""},
	}
	for _, c := range cases {
		out := mustApply(t, d, c.ops...)
		s, _ := Impact(d, out, nil)
		var ids []string
		for _, a := range s.Affected {
			ids = append(ids, a.ID)
		}
		if c.want == "" {
			if !s.Empty() && len(s.Affected) != 0 {
				t.Errorf("%s: %v", c.name, ids)
			}
			continue
		}
		if len(ids) == 0 {
			t.Errorf("%s: nothing affected", c.name)
			continue
		}
		got := map[string]bool{}
		for _, id := range ids {
			got[id] = true
		}
		for _, id := range strings.Split(c.want, ",") {
			if !got[id] {
				t.Errorf("%s: %s should be affected (got %v)", c.name, id, ids)
			}
		}
		if c.tests != "" {
			var tests []string
			for _, tr := range s.Tests {
				tests = append(tests, tr.Criterion)
			}
			if strings.Join(tests, ",") != c.tests {
				t.Errorf("%s: tests %v, want %s", c.name, tests, c.tests)
			}
		}
	}
}

// a change with no effect on the design has no impact at all: this is what lets the
// reconciler close a wording-only edit as "no change"
func TestAWordingOnlyEditHasNoImpact(t *testing.T) {
	d := grocery(t)
	out := mustApply(t, d,
		PatchOp{Op: "text", Item: "local-payments", Text: "Pay by card or transfer."},
		PatchOp{Op: "add", Parent: "decisions", Text: "Weekly is the default."},
		PatchOp{Op: "answer", Item: "q-auto-charge", Answer: sp("b")},
	)
	s, _ := Impact(d, out, nil)
	if !s.Delta.Empty() {
		t.Fatalf("%+v", s.Delta)
	}
	// and Diff agrees that none of it was semantic... except the answer
	if sem := Semantic(Diff(d, out)); len(sem) == 0 {
		t.Fatal("answering a question is a spec change")
	}
}

// whatever patch is applied, the impact is self-consistent: each thing once, every
// changed node is affected, tests belong to affected capabilities, the work has no
// repeats, and comparing a design with itself affects nothing
func TestImpactOfRandomPatchesIsConsistent(t *testing.T) {
	base := grocery(t)
	for i := 0; i < 300; i++ {
		d := base
		for tries := 0; tries < 6; tries++ {
			if out, err := Apply(d, SpecPatch{Ops: []PatchOp{randomOp(rngFor(i+1000), d, i, tries)}}); err == nil {
				d = out
			}
		}
		s, _ := Impact(base, d, nil)
		seen := map[string]bool{}
		for _, a := range s.Affected {
			if seen[a.ID] {
				t.Fatalf("iteration %d: %s listed twice", i, a.ID)
			}
			seen[a.ID] = true
		}
		for _, c := range s.Delta.Nodes {
			if !seen[c.ID] {
				t.Fatalf("iteration %d: %s changed but is not affected", i, c.ID)
			}
		}
		for _, tr := range s.Tests {
			if !seen[tr.Capability] {
				t.Fatalf("iteration %d: test %v belongs to a capability that is not affected", i, tr)
			}
		}
		titles := map[string]bool{}
		for _, w := range s.Work {
			key := w.Kind + "/" + w.Node + "/" + w.Title // two different things may share a name
			if titles[key] {
				t.Fatalf("iteration %d: %q is there twice", i, w.Title)
			}
			titles[key] = true
		}
		if self, _ := Impact(d, d, nil); !self.Empty() {
			t.Fatalf("iteration %d: a design compared with itself", i)
		}
		if s.Delta.Empty() != (len(s.Affected) == 0) {
			t.Fatalf("iteration %d: delta empty=%v but %d affected", i, s.Delta.Empty(), len(s.Affected))
		}
	}
}

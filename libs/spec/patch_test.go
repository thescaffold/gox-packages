package spec

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
)

func grocery(t *testing.T) *Doc {
	t.Helper()
	d, _ := Parse(read(t, "testdata/valid/grocery.ospec"))
	return d
}

func sp(s string) *string { return &s }

func mustApply(t *testing.T, d *Doc, ops ...PatchOp) *Doc {
	t.Helper()
	out, err := Apply(d, SpecPatch{Ops: ops})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return out
}

func applyErr(t *testing.T, d *Doc, ops ...PatchOp) string {
	t.Helper()
	before := Print(d)
	out, err := Apply(d, SpecPatch{Ops: ops})
	if err == nil || out != nil {
		t.Fatalf("expected an error, got a document")
	}
	if Print(d) != before {
		t.Fatalf("a failed patch changed the original")
	}
	return err.Error()
}

// App. E.5: the example patch applies to the E.6 example
func TestTheAppendixPatchAppliesToTheReferenceExample(t *testing.T) {
	d := grocery(t)
	p, err := ParsePatch([]byte(read(t, "testdata/patches/orders-service.json")))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Check(true); err != nil {
		t.Fatal(err)
	}
	out, err := Apply(d, p)
	if err != nil {
		t.Fatal(err)
	}
	text := Print(out)
	for _, want := range []string{
		"## Parts\n\n### Orders {#orders-service}\ndoes: [[#recurring-orders]]\nowns: [[#order]], [[#order-line]]\n",
		"uses: [[Product]]\n",
		"needs: [[#orders-service]]\n",
		"- Orders is a separate service so it can scale on its own. (@you) {#d-orders-is-a-separate-service}",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "uses: [[Order]]") {
		t.Errorf("the removed reference is still there")
	}
	if HasErrors(Validate(out)) {
		t.Errorf("the result is broken: %v", Validate(out))
	}
	// the original is untouched, and what came out reads back the same
	if Print(d) != read(t, "testdata/valid/grocery.ospec") {
		t.Errorf("the original document changed")
	}
	again, _ := Parse(text)
	if Print(again) != text {
		t.Errorf("the result does not read back the same")
	}
	// and the patch can be undone exactly
	inv, err := Invert(d, p)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Apply(out, inv)
	if err != nil {
		t.Fatal(err)
	}
	if Print(back) != Print(d) {
		t.Errorf("undo did not restore the document:\n%s", Print(back))
	}
}

func TestPatchesAreAtomic(t *testing.T) {
	d := grocery(t)
	msg := applyErr(t, d,
		PatchOp{Op: "add", Parent: "decisions", Text: "A fine decision."},
		PatchOp{Op: "set", Item: "recurring-orders", Prop: "priority", Value: &Strings{"should"}},
		PatchOp{Op: "remove", Item: "no-such-item"},
	)
	if !strings.Contains(msg, "step 3 (remove)") || !strings.Contains(msg, "no-such-item") {
		t.Fatalf("the error does not say which step failed: %s", msg)
	}
	// the same two good steps alone do apply, so it was step 3 that stopped them
	mustApply(t, d,
		PatchOp{Op: "add", Parent: "decisions", Text: "A fine decision."},
		PatchOp{Op: "set", Item: "recurring-orders", Prop: "priority", Value: &Strings{"should"}},
	)
}

func TestEachOperationDoesItsOneThing(t *testing.T) {
	d := grocery(t)
	find := func(doc *Doc, id string) *Item {
		l, ok := doc.locate(id)
		if !ok || l.item == nil {
			t.Fatalf("no item %s", id)
		}
		return l.item
	}

	t.Run("set replaces, adds and removes list elements, and drops an empty property", func(t *testing.T) {
		o := mustApply(t, d, PatchOp{Op: "set", Item: "recurring-orders", Prop: "uses", Add: Strings{"[[Product]]", "[[#paystack]]"}})
		if got := propValue(find(o, "recurring-orders"), "uses"); got != "[[Order]], [[Product]], [[#paystack]]" {
			t.Fatalf("add: %q", got)
		}
		o = mustApply(t, d, PatchOp{Op: "set", Item: "recurring-orders", Prop: "uses", Remove: Strings{"[[#order]]", "[[#product]]"}})
		if got := propValue(find(o, "recurring-orders"), "uses"); got != "" {
			t.Fatalf("a property with nothing left should be gone, got %q", got)
		}
		o = mustApply(t, d, PatchOp{Op: "set", Item: "local-payments", Prop: "shows", Value: &Strings{"[[My orders]]"}})
		if got := propValue(find(o, "local-payments"), "shows"); got != "[[My orders]]" {
			t.Fatalf("new property: %q", got)
		}
	})

	t.Run("text changes the description", func(t *testing.T) {
		o := mustApply(t, d, PatchOp{Op: "text", Item: "recurring-orders", Text: "Orders repeat on a schedule."})
		if !strings.Contains(Print(o), "### Recurring orders {#recurring-orders}\nOrders repeat on a schedule.\nfor:") {
			t.Fatalf("%s", Print(o))
		}
		o = mustApply(t, d, PatchOp{Op: "text", Item: "c-3f2a", Text: "A household sets Milk to repeat weekly."})
		if !strings.Contains(Print(o), "- [ ] A household sets Milk to repeat weekly. {#c-3f2a}") {
			t.Fatalf("a criterion's text: %s", Print(o))
		}
		o = mustApply(t, d, PatchOp{Op: "text", Item: "d-retry", Text: "2026-09-21 — Failed payments retry twice. (@isaiah)"})
		if !strings.Contains(Print(o), "retry twice") {
			t.Fatalf("a decision's text")
		}
	})

	t.Run("answer needs one of the choices", func(t *testing.T) {
		o := mustApply(t, d, PatchOp{Op: "answer", Item: "q-auto-charge", Answer: sp("b")})
		if !strings.Contains(Print(o), "answer: b\n") {
			t.Fatalf("%s", Print(o))
		}
		if msg := applyErr(t, d, PatchOp{Op: "answer", Item: "q-auto-charge", Answer: sp("z")}); !strings.Contains(msg, "not one of the choices") {
			t.Fatal(msg)
		}
		o = mustApply(t, d, PatchOp{Op: "answer", Item: "q-auto-charge", Answer: sp("")})
		if strings.Contains(Print(o), "answer:") {
			t.Fatalf("clearing: %s", Print(o))
		}
	})

	t.Run("rename keeps the id and the references", func(t *testing.T) {
		o := mustApply(t, d, PatchOp{Op: "rename", Item: "household", Title: "Family"})
		text := Print(o)
		if !strings.Contains(text, "- **Family** {#household}:") || !strings.Contains(text, "for: [[Family]]") || !strings.Contains(text, "household: [[Family]]") {
			t.Fatalf("%s", text)
		}
		if strings.Contains(text, "[[Household]]") {
			t.Fatalf("an old reference is left")
		}
	})

	t.Run("remove takes out an item, a criterion or a field", func(t *testing.T) {
		o := mustApply(t, d, PatchOp{Op: "remove", Item: "product"})
		if _, ok := o.locate("product"); ok {
			t.Fatal("still there")
		}
		o = mustApply(t, d, PatchOp{Op: "remove", Item: "c-b310"})
		if strings.Contains(Print(o), "Skipping the next order removes it") {
			t.Fatal("criterion still there")
		}
		o = mustApply(t, d, PatchOp{Op: "remove", Item: "c-1e08"}) // the only criterion of local-payments
		if strings.Contains(Print(o), "done when:\n- [ ] A test payment") {
			t.Fatal("the last criterion is still there")
		}
	})

	t.Run("add puts an entry in a list, and a field on an entity", func(t *testing.T) {
		o := mustApply(t, d, PatchOp{Op: "add", Parent: "local-payments", List: "done when", Text: "A refund returns the money.", ID: "c-refund"})
		if !strings.Contains(Print(o), "- [ ] A test payment succeeds and the order is marked paid. {#c-1e08}\n- [ ] A refund returns the money. {#c-refund}") {
			t.Fatalf("%s", Print(o))
		}
		o = mustApply(t, d, PatchOp{Op: "add", Parent: "product", List: "fields", Text: "stock: number", After: sp("")})
		if !strings.Contains(Print(o), "### Product {#product}\n- stock: number\n- name: text") {
			t.Fatalf("%s", Print(o))
		}
		o = mustApply(t, d, PatchOp{Op: "add", Parent: "questions", Kind: "question", Text: "Which currency?", Options: []string{"NGN", "USD"}})
		if !strings.Contains(Print(o), "? Which currency? {#q-which-currency}\n- (a) NGN\n- (b) USD") {
			t.Fatalf("%s", Print(o))
		}
	})

	t.Run("move places an item, and an entry, where it is told", func(t *testing.T) {
		o := mustApply(t, d, PatchOp{Op: "move", Item: "local-payments", After: sp("")})
		feats := d2(o, "Features")
		if feats[0] != "local-payments" || feats[1] != "recurring-orders" {
			t.Fatalf("order: %v", feats)
		}
		// a criterion moves to another feature's list, after the one it names
		o = mustApply(t, d, PatchOp{Op: "move", Item: "c-1e08", Parent: "recurring-orders", After: sp("c-3f2a")})
		if got := entryIDs(o, "recurring-orders", "done when"); strings.Join(got, ",") != "c-3f2a,c-1e08,c-b310,c-52c9" {
			t.Fatalf("done when: %v", got)
		}
		if _, ok := o.locate("c-1e08"); !ok || len(entryIDs(o, "local-payments", "done when")) != 0 {
			t.Fatalf("the emptied list should be gone")
		}
		// and within one list, to the front
		o = mustApply(t, d, PatchOp{Op: "move", Item: "c-52c9", After: sp("")})
		if got := entryIDs(o, "recurring-orders", "done when"); strings.Join(got, ",") != "c-52c9,c-3f2a,c-b310" {
			t.Fatalf("reorder: %v", got)
		}
	})

	t.Run("split makes new items next to the first", func(t *testing.T) {
		o := mustApply(t, d, PatchOp{Op: "split", Item: "recurring-orders", Into: []PatchOp{
			{Op: "add", ID: "skip-order", Title: "Skip the next order", Text: "One tap."},
			{Op: "add", ID: "change-frequency", Title: "Change how often"},
		}})
		if got := d2(o, "Features"); strings.Join(got, ",") != "recurring-orders,skip-order,change-frequency,local-payments" {
			t.Fatalf("%v", got)
		}
	})

	t.Run("merge joins items and points references at the one that is left", func(t *testing.T) {
		o := mustApply(t, d, PatchOp{Op: "merge", Items: []string{"order", "order-line"}, Title: "Order"})
		if _, ok := o.locate("order-line"); ok {
			t.Fatal("the merged item is still there")
		}
		text := Print(o)
		if !strings.Contains(text, "- product: [[Product]]") || !strings.Contains(text, "- household: [[Household]]") {
			t.Fatalf("fields were not joined:\n%s", text)
		}
		if strings.Contains(text, "[[Order line]]") {
			t.Fatalf("a reference to the merged item is left:\n%s", text)
		}
		if !strings.Contains(text, "items: many [[Order]]") {
			t.Fatalf("the reference was not pointed at the survivor:\n%s", text)
		}
	})
}

func d2(d *Doc, section string) []string {
	var ids []string
	if s := d.findSection(section); s != nil {
		ids = sectionIDs(s)
	}
	return ids
}

func entryIDs(d *Doc, item, key string) []string {
	l, ok := d.locate(item)
	if !ok || l.item == nil {
		return nil
	}
	var ids []string
	for _, b := range l.item.Body {
		if lp, ok := b.(*ListProp); ok && lp.Key == key {
			for _, e := range lp.Entries {
				ids = append(ids, e.ID)
			}
		}
	}
	return ids
}

func propValue(it *Item, key string) string {
	for _, b := range it.Body {
		if p, ok := b.(*Prop); ok && p.Key == key {
			return p.Value
		}
	}
	return ""
}

func TestPatchesThatAreWrongSayWhy(t *testing.T) {
	d := grocery(t)
	cases := []struct {
		name string
		op   PatchOp
		want string
	}{
		{"unknown item", PatchOp{Op: "text", Item: "nope", Text: "x"}, "no item “nope”"},
		{"id taken", PatchOp{Op: "add", Parent: "features", ID: "order", Title: "X"}, "already used"},
		{"bad id", PatchOp{Op: "add", Parent: "features", ID: "no good", Title: "X"}, "cannot be an id"},
		{"wrong kind for the section", PatchOp{Op: "add", Parent: "features", Kind: "user", Title: "X"}, "holds features"},
		{"unknown section", PatchOp{Op: "add", Parent: "gadgets", Title: "X"}, "neither a section nor an item"},
		{"a block item needs a title", PatchOp{Op: "add", Parent: "features", Text: "x"}, "needs a title"},
		{"a user needs a name", PatchOp{Op: "add", Parent: "users", Text: "x"}, "needs a name"},
		{"a one-line item cannot carry properties", PatchOp{Op: "add", Parent: "decisions", Text: "x", Props: map[string]Strings{"for": {"y"}}}, "cannot carry properties"},
		{"remove from a property that is not there", PatchOp{Op: "set", Item: "recurring-orders", Prop: "uses", Remove: Strings{"[[Nothing]]"}}, "is not in “uses”"},
		{"set on a list", PatchOp{Op: "set", Item: "recurring-orders", Prop: "must", Value: &Strings{"x"}}, "is a list"},
		{"set on a one-line item", PatchOp{Op: "set", Item: "household", Prop: "for", Value: &Strings{"x"}}, "no properties"},
		{"a multi-line title", PatchOp{Op: "rename", Item: "household", Title: "a\nb"}, "one line"},
		{"move to another kind of section", PatchOp{Op: "move", Item: "household", Parent: "features"}, "cannot move"},
		{"move after itself", PatchOp{Op: "move", Item: "order", After: sp("order")}, "after itself"},
		{"move without saying where", PatchOp{Op: "move", Item: "order"}, "does not say where"},
		{"answer something that is not a question", PatchOp{Op: "answer", Item: "order", Answer: sp("a")}, "not a question"},
		{"merge across sections", PatchOp{Op: "merge", Items: []string{"order", "household"}}, "different sections"},
		{"merge a question", PatchOp{Op: "merge", Items: []string{"q-auto-charge", "q-auto-charge"}}, "listed twice"},
		{"unknown operation", PatchOp{Op: "explode", Item: "order"}, "not something a patch can do"},
		{"split with nothing to make", PatchOp{Op: "split", Item: "order"}, "needs the new items"},
		{"a list entry that is not a list", PatchOp{Op: "add", Parent: "recurring-orders", List: "colours", Text: "x"}, "“must”"},
	}
	for _, c := range cases {
		msg := applyErr(t, d, c.op)
		if !strings.Contains(msg, c.want) {
			t.Errorf("%s: %q does not mention %q", c.name, msg, c.want)
		}
	}
}

func TestACheckedPatchNeedsReasonsAndTargets(t *testing.T) {
	good := SpecPatch{Ops: []PatchOp{{Op: "remove", Item: "x", Reason: "because"}}}
	if err := good.Check(true); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]SpecPatch{
		"no steps":   {},
		"no reason":  {Ops: []PatchOp{{Op: "remove", Item: "x"}}},
		"no target":  {Ops: []PatchOp{{Op: "remove", Reason: "r"}}},
		"no parent":  {Ops: []PatchOp{{Op: "add", Reason: "r"}}},
		"merge of 1": {Ops: []PatchOp{{Op: "merge", Items: []string{"a"}, Reason: "r"}}},
		"strange op": {Ops: []PatchOp{{Op: "fly", Reason: "r"}}},
	} {
		if err := p.Check(true); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if err := (SpecPatch{Ops: []PatchOp{{Op: "remove", Item: "x"}}}).Check(false); err != nil {
		t.Errorf("reasons are only needed when asked for: %v", err)
	}
}

func TestPatchJSON(t *testing.T) {
	p, err := ParsePatch([]byte(`{"base":3,"ops":[{"op":"add","parent":"limits","text":"x","props":{"for":"one","needs":["a","b"]},"after":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	op := p.Ops[0]
	if p.Base != 3 || len(op.Props["for"]) != 1 || len(op.Props["needs"]) != 2 || op.After == nil || *op.After != "" {
		t.Fatalf("%+v", op)
	}
	if _, err := ParsePatch([]byte(`{"ops":[{"op":"add","props":{"for":7}}]}`)); err == nil {
		t.Fatal("a number is not a string or a list")
	}
	b, _ := json.Marshal(SpecPatch{Ops: []PatchOp{{Op: "set", Item: "x", Prop: "for", Value: &Strings{"a"}}}})
	if !strings.Contains(string(b), `"value":["a"]`) {
		t.Fatalf("%s", b)
	}
}

// the undo of any patch restores the document exactly
func TestInvertThenApplyIsTheIdentity(t *testing.T) {
	d := grocery(t)
	patches := map[string][]PatchOp{
		"add to a new section":       {{Op: "add", Parent: "parts", Title: "Shop app", Props: map[string]Strings{"does": {"[[Recurring orders]]"}}}},
		"add to an existing section": {{Op: "add", Parent: "limits", Text: "cost: under 5 dollars a month"}},
		"set":                        {{Op: "set", Item: "recurring-orders", Prop: "priority", Value: &Strings{"should"}}},
		"text":                       {{Op: "text", Item: "recurring-orders", Text: "New words.\nOn two lines."}},
		"answer":                     {{Op: "answer", Item: "q-auto-charge", Answer: sp("b")}},
		"rename with references":     {{Op: "rename", Item: "household", Title: "Family"}},
		"remove an item":             {{Op: "remove", Item: "product"}},
		"remove a criterion":         {{Op: "remove", Item: "c-1e08"}},
		"move to the front":          {{Op: "move", Item: "local-payments", After: sp("")}},
		"split":                      {{Op: "split", Item: "recurring-orders", Into: []PatchOp{{Add: nil, Op: "add", Title: "Skip it", ID: "skip-it"}}}},
		"merge":                      {{Op: "merge", Items: []string{"order", "order-line"}, Title: "Order"}},
		"several steps": {
			{Op: "add", Parent: "decisions", Text: "x"},
			{Op: "move", Item: "my-orders", After: sp("")},
			{Op: "remove", Item: "paystack"},
			{Op: "rename", Item: "product", Title: "Goods"},
		},
	}
	for name, ops := range patches {
		p := SpecPatch{Base: 1, Summary: name, Ops: ops}
		inv, err := Invert(d, p)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		out := mustApply(t, d, ops...)
		back, err := Apply(out, inv)
		if err != nil {
			t.Errorf("%s: the undo does not apply: %v", name, err)
			continue
		}
		if Print(back) != Print(d) {
			t.Errorf("%s: undo did not restore the document\n--- want\n%s\n--- got\n%s", name, Print(d), Print(back))
		}
		// and the undo of the undo redoes it
		redo, err := Invert(out, inv)
		if err != nil {
			t.Errorf("%s: invert of the undo: %v", name, err)
			continue
		}
		again, err := Apply(back, redo)
		if err != nil || Print(again) != Print(out) {
			t.Errorf("%s: redo failed: %v", name, err)
		}
	}
	// a document with items that have no id yet cannot be undone exactly, and says so
	loose, _ := Parse("## Features\n### Thing\n")
	if _, err := Invert(loose, SpecPatch{Ops: []PatchOp{{Op: "add", Parent: "limits", Text: "x"}}}); err == nil || !strings.Contains(err.Error(), "no id yet") {
		t.Errorf("err = %v", err)
	}
}

// random patches: a patch either applies or leaves the document alone, and one
// that applies can be undone exactly
func TestRandomPatchesAreAtomicAndInvertible(t *testing.T) {
	base := grocery(t)
	rng := rand.New(rand.NewSource(7))
	applied, failed := 0, 0
	for iter := 0; iter < 400; iter++ {
		d := base
		var ops []PatchOp
		for n := 1 + rng.Intn(4); n > 0; n-- {
			ops = append(ops, randomOp(rng, d, iter, n))
		}
		before := Print(d)
		out, err := Apply(d, SpecPatch{Ops: ops})
		if Print(d) != before {
			t.Fatalf("iteration %d: Apply changed its input", iter)
		}
		if err != nil {
			failed++
			if out != nil {
				t.Fatalf("iteration %d: an error and a document", iter)
			}
			continue
		}
		applied++
		if HasErrors(Validate(out)) {
			t.Fatalf("iteration %d: the result is broken: %v\n%s", iter, Validate(out), mustJSON(ops))
		}
		inv, err := Invert(d, SpecPatch{Ops: ops})
		if err != nil {
			t.Fatalf("iteration %d: Invert: %v\n%s", iter, err, mustJSON(ops))
		}
		back, err := Apply(out, inv)
		if err != nil || Print(back) != before {
			t.Fatalf("iteration %d: undo failed (%v)\n%s", iter, err, mustJSON(ops))
		}
	}
	if applied < 100 || failed < 20 {
		t.Fatalf("the generator is not exercising both outcomes: %d applied, %d failed", applied, failed)
	}
}

func mustJSON(v any) string { b, _ := json.MarshalIndent(v, "", " "); return string(b) }

func randomOp(rng *rand.Rand, d *Doc, iter, n int) PatchOp {
	var ids []string
	for _, a := range d.Addressables() {
		ids = append(ids, a.ID)
	}
	pick := func() string { return ids[rng.Intn(len(ids))] }
	sections := []string{"features", "limits", "users", "decisions", "parts", "data", "questions", "notes", "screens"}
	switch rng.Intn(11) {
	case 0:
		return PatchOp{Op: "add", Parent: sections[rng.Intn(len(sections))], Title: fmt.Sprintf("Thing %d %d", iter, n), Text: "words"}
	case 1:
		return PatchOp{Op: "add", Parent: "decisions", Text: fmt.Sprintf("Decided %d.%d", iter, n)}
	case 2:
		return PatchOp{Op: "set", Item: pick(), Prop: "uses", Add: Strings{"[[Product]]"}}
	case 3:
		return PatchOp{Op: "set", Item: pick(), Prop: "priority", Value: &Strings{"could"}}
	case 4:
		return PatchOp{Op: "text", Item: pick(), Text: fmt.Sprintf("Reworded %d", iter)}
	case 5:
		return PatchOp{Op: "remove", Item: pick()}
	case 6:
		return PatchOp{Op: "rename", Item: pick(), Title: fmt.Sprintf("Renamed %d", iter)}
	case 7:
		return PatchOp{Op: "move", Item: pick(), After: sp(pick())}
	case 8:
		return PatchOp{Op: "merge", Items: []string{pick(), pick()}}
	case 9:
		return PatchOp{Op: "split", Item: pick(), Into: []PatchOp{{Op: "add", Title: fmt.Sprintf("Part %d", iter)}}}
	}
	return PatchOp{Op: "add", Parent: pick(), List: "must", Text: fmt.Sprintf("A rule %d", iter)}
}

// whatever bytes arrive as a patch: it either fails cleanly and leaves the
// document alone, or applies, leaves a document that reads back the same and has
// no structural error, and can be undone exactly
func FuzzApplyPatch(f *testing.F) {
	src, err := os.ReadFile("testdata/valid/grocery.ospec")
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{
		`{"ops":[{"op":"add","parent":"limits","text":"x"}]}`,
		`{"ops":[{"op":"rename","item":"household","title":"Family"}]}`,
		`{"ops":[{"op":"merge","items":["order","order-line"],"title":"Order"}]}`,
		`{"ops":[{"op":"move","item":"c-52c9","parent":"local-payments","after":""}]}`,
		`{"ops":[{"op":"split","item":"order","into":[{"op":"add","title":"A"},{"op":"add","title":"B"}]}]}`,
		`{"ops":[{"op":"set","item":"recurring-orders","prop":"uses","remove":["[[#order]]"],"add":"[[#paystack]]"}]}`,
		`{"ops":[{"op":"add","parent":"parts","kind":"part","id":"p","title":"P","props":{"does":["[[#order]]"],"must":["one","two"]}}]}`,
		`{"ops":[{"op":"remove","item":"product"},{"op":"remove","item":"c-1e08"}]}`,
		`{"ops":[{"op":"answer","item":"q-auto-charge","answer":"b"},{"op":"text","item":"recurring-orders","text":"a\nb"}]}`,
	} {
		f.Add([]byte(s))
	}
	if b, err := os.ReadFile("testdata/patches/orders-service.json"); err == nil {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		p, err := ParsePatch(in)
		if err != nil {
			return
		}
		d, _ := Parse(string(src))
		before := Print(d)
		out, err := Apply(d, p)
		if Print(d) != before {
			t.Fatalf("Apply changed its input\n%s", in)
		}
		if err != nil {
			if out != nil {
				t.Fatalf("an error and a document\n%s", in)
			}
			return
		}
		if HasErrors(Validate(out)) {
			t.Fatalf("the result is broken: %v\n%s", Validate(out), in)
		}
		text := Print(out)
		again, _ := Parse(text)
		if Print(again) != text {
			t.Fatalf("the result does not read back the same\n%s\n--- first\n%s\n--- second\n%s", in, text, Print(again))
		}
		inv, err := Invert(d, p)
		if err != nil {
			t.Fatalf("a patch that applies must be invertible: %v\n%s", err, in)
		}
		back, err := Apply(out, inv)
		if err != nil || Print(back) != before {
			t.Fatalf("undo failed (%v)\n%s\n--- got\n%s", err, in, Print(back))
		}
	})
}

// a patch can leave an empty section behind; undoing it removes the section, and
// undoing that makes it again
func TestAnEmptySectionIsUndoneAndRedone(t *testing.T) {
	d := grocery(t)
	ops := []PatchOp{{Op: "add", Parent: "parts", Title: "Shop", ID: "shop"}, {Op: "remove", Item: "shop"}}
	out := mustApply(t, d, ops...)
	if s := out.findSection("Parts"); s == nil || len(s.Nodes) != 0 {
		t.Fatal("the empty section should be there")
	}
	inv, err := Invert(d, SpecPatch{Ops: ops})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Ops[len(inv.Ops)-1].Op != "prune" {
		t.Fatalf("%v", inv.Ops)
	}
	back := mustApply(t, out, inv.Ops...)
	if Print(back) != Print(d) {
		t.Fatal("undo did not remove the empty section")
	}
	redo, err := Invert(out, inv)
	if err != nil {
		t.Fatal(err)
	}
	if redo.Ops[0].Op != "section" {
		t.Fatalf("%v", redo.Ops)
	}
	if again := mustApply(t, back, redo.Ops...); Print(again) != Print(out) {
		t.Fatal("redo did not make the section again")
	}
}

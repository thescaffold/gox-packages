package spec

import (
	"math/rand"
	"sort"
	"strings"
	"testing"
)

func fieldsOfConflicts(cs []Conflict) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.ID+"/"+c.Field)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestMergeTakesEachSidesOwnChanges(t *testing.T) {
	base := grocery(t)
	mine := mustApply(t, base,
		PatchOp{Op: "set", Item: "recurring-orders", Prop: "priority", Value: &Strings{"should"}},
		PatchOp{Op: "add", Parent: "limits", Text: "cost: cheap", ID: "c-cost"},
	)
	theirs := mustApply(t, base,
		PatchOp{Op: "text", Item: "local-payments", Text: "Pay by card or transfer."},
		PatchOp{Op: "set", Item: "recurring-orders", Prop: "uses", Add: Strings{"[[#paystack]]"}},
		PatchOp{Op: "add", Parent: "recurring-orders", List: "done when", Text: "A refund works.", ID: "c-refund"},
		PatchOp{Op: "remove", Item: "c-b310"},
		PatchOp{Op: "add", Parent: "decisions", Text: "Weekly is the default.", ID: "d-weekly"},
	)
	out, conflicts := Merge3(base, mine, theirs)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts: %v", fieldsOfConflicts(conflicts))
	}
	text := Print(out)
	for _, want := range []string{
		"priority: should", "uses: [[Order]], [[Product]], [[#paystack]]", "Pay by card or transfer.",
		"- [ ] A refund works. {#c-refund}", "- cost: cheap {#c-cost}", "Weekly is the default. {#d-weekly}",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "{#c-b310}") {
		t.Errorf("the criterion they removed is still there")
	}
	if HasErrors(Validate(out)) {
		t.Errorf("the merge is broken: %v", Validate(out))
	}
	// the decision they added lands after the one that was last before it
	if got := d2(out, "Decisions"); strings.Join(got, ",") != "d-retry,d-weekly" {
		t.Errorf("decisions: %v", got)
	}
}

func TestMergeWithNothingOnOneSideTakesTheOther(t *testing.T) {
	base := grocery(t)
	changed := mustApply(t, base,
		PatchOp{Op: "rename", Item: "household", Title: "Family"},
		PatchOp{Op: "remove", Item: "product"},
		PatchOp{Op: "add", Parent: "parts", Title: "Shop", ID: "shop"},
		PatchOp{Op: "move", Item: "local-payments", After: sp("")},
	)
	for name, got := range map[string]*Doc{
		"theirs unchanged": first(Merge3(base, changed, base)),
		"mine unchanged":   first(Merge3(base, base, changed)),
		"same on both":     first(Merge3(base, changed, changed.Clone())),
	} {
		if name == "mine unchanged" {
			// order is mine's, so the move to the front is not carried over; everything else is
			if len(Diff(changed, got)) != 0 && len(Semantic(Diff(changed, got))) != 0 {
				t.Errorf("%s: %v", name, summarize(Diff(changed, got)))
			}
			continue
		}
		if Print(got) != Print(changed) {
			t.Errorf("%s:\n--- want\n%s\n--- got\n%s", name, Print(changed), Print(got))
		}
	}
}

func first(d *Doc, _ []Conflict) *Doc { return d }

func TestMergeReportsWhatBothSidesChangedDifferently(t *testing.T) {
	base := grocery(t)
	mine := mustApply(t, base,
		PatchOp{Op: "set", Item: "recurring-orders", Prop: "priority", Value: &Strings{"should"}},
		PatchOp{Op: "rename", Item: "household", Title: "Family"},
		PatchOp{Op: "text", Item: "c-91ab", Text: "An item can repeat every 1 to 12 weeks."},
		PatchOp{Op: "answer", Item: "q-auto-charge", Answer: sp("b")},
		PatchOp{Op: "remove", Item: "c-52c9"},
		PatchOp{Op: "text", Item: "local-payments", Text: "Mine."},
	)
	theirs := mustApply(t, base,
		PatchOp{Op: "set", Item: "recurring-orders", Prop: "priority", Value: &Strings{"could"}},
		PatchOp{Op: "rename", Item: "household", Title: "Customer"},
		PatchOp{Op: "text", Item: "c-91ab", Text: "An item can repeat every 1 to 4 weeks."},
		PatchOp{Op: "answer", Item: "q-auto-charge", Answer: sp("a")}, // unchanged from base: not a change at all
		PatchOp{Op: "text", Item: "c-52c9", Text: "Their rewording."},
		PatchOp{Op: "remove", Item: "local-payments"},
	)
	out, conflicts := Merge3(base, mine, theirs)
	want := "household/title,local-payments/item,recurring-orders/entry:done when:c-52c9,recurring-orders/entry:must:c-91ab,recurring-orders/prop:priority"
	if got := fieldsOfConflicts(conflicts); got != want {
		t.Fatalf("conflicts:\n got %s\nwant %s", got, want)
	}
	// "mine" stays wherever there is a conflict, and their unconflicted answer (none) leaves mine's
	text := Print(out)
	for _, keep := range []string{"priority: should", "**Family**", "1 to 12 weeks", "Mine.", "answer: b"} {
		if !strings.Contains(text, keep) {
			t.Errorf("mine was not kept: missing %q", keep)
		}
	}
	for _, c := range conflicts {
		if c.Reason == "" {
			t.Errorf("a conflict with no reason: %+v", c)
		}
		if c.ID == "household" && (c.Base != "Household" || c.Mine != "Family" || c.Theirs != "Customer") {
			t.Errorf("the conflict does not show the three versions: %+v", c)
		}
	}
}

func TestMergeDeletionAgainstEdit(t *testing.T) {
	base := grocery(t)
	// they delete something I did not touch: it goes
	theirs := mustApply(t, base, PatchOp{Op: "remove", Item: "product"})
	out, conflicts := Merge3(base, base, theirs)
	if _, ok := out.locate("product"); ok || len(conflicts) != 0 {
		t.Fatalf("a deletion of an untouched item should apply cleanly: %v", fieldsOfConflicts(conflicts))
	}
	// I delete something they did not touch: it stays gone
	out, conflicts = Merge3(base, theirs, base)
	if _, ok := out.locate("product"); ok || len(conflicts) != 0 {
		t.Fatalf("my deletion should stand: %v", fieldsOfConflicts(conflicts))
	}
	// each deletes what the other changed
	edited := mustApply(t, base, PatchOp{Op: "text", Item: "c-1e08", Text: "A payment works."}, PatchOp{Op: "set", Item: "product", Prop: "for", Value: &Strings{"x"}})
	_, conflicts = Merge3(base, edited, theirs)
	if fieldsOfConflicts(conflicts) != "product/item" {
		t.Fatalf("%v", fieldsOfConflicts(conflicts))
	}
	_, conflicts = Merge3(base, theirs, edited)
	if fieldsOfConflicts(conflicts) != "product/item" {
		t.Fatalf("%v", fieldsOfConflicts(conflicts))
	}
}

func TestMergeSettingsAndNotes(t *testing.T) {
	base, _ := Parse("ospec: 1\n# T\n> S\n\nplatform: web\ncurrency: NGN\n\n## Notes\nOld.\n")
	mine, _ := Parse("ospec: 1\n# T\n> S\n\nplatform: web, mobile\ncurrency: NGN\n\n## Notes\nOld.\n")
	theirs, _ := Parse("ospec: 1\n# T2\n> S\n\nplatform: web\ncurrency: USD\nmarket: Ghana\n\n## Notes\nNew words.\n")
	out, conflicts := Merge3(base, mine, theirs)
	if len(conflicts) != 0 {
		t.Fatalf("%v", fieldsOfConflicts(conflicts))
	}
	text := Print(out)
	for _, want := range []string{"# T2", "platform: web, mobile", "currency: USD", "market: Ghana", "New words."} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	mine2, _ := Parse("ospec: 1\n# T\n> S\n\nplatform: web\ncurrency: EUR\n\n## Notes\nMy words.\n")
	_, conflicts = Merge3(base, mine2, theirs)
	if got := fieldsOfConflicts(conflicts); got != "/notes,/setting:currency" {
		t.Fatalf("%s", got)
	}
}

func TestMergeBothAddingTheSameIDIsAConflictUnlessTheyAgree(t *testing.T) {
	base := grocery(t)
	a := mustApply(t, base, PatchOp{Op: "add", Parent: "features", Title: "Gift orders", ID: "gifts", Text: "Mine."})
	b := mustApply(t, base, PatchOp{Op: "add", Parent: "features", Title: "Gift orders", ID: "gifts", Text: "Theirs."})
	_, conflicts := Merge3(base, a, b)
	if fieldsOfConflicts(conflicts) != "gifts/item" {
		t.Fatalf("%v", fieldsOfConflicts(conflicts))
	}
	out, conflicts := Merge3(base, a, a.Clone())
	if len(conflicts) != 0 || Print(out) != Print(a) {
		t.Fatalf("the same addition on both sides should merge cleanly")
	}
}

// random pairs of patches: the merge never produces a broken document, and when
// it reports no conflicts the two orders agree on what is in it
func TestRandomMerges(t *testing.T) {
	base := grocery(t)
	rng := rand.New(rand.NewSource(11))
	clean, conflicted := 0, 0
	for iter := 0; iter < 800; iter++ {
		var sideOps [][]PatchOp
		side := func() *Doc {
			for tries := 0; tries < 20; tries++ {
				var ops []PatchOp
				for n := 1 + rng.Intn(3); n > 0; n-- {
					ops = append(ops, randomOp(rng, base, iter, n))
				}
				if d, err := Apply(base, SpecPatch{Ops: ops}); err == nil {
					sideOps = append(sideOps, ops)
					return d
				}
			}
			sideOps = append(sideOps, nil)
			return base
		}
		mine, theirs := side(), side()
		out, conflicts := Merge3(base, mine, theirs)
		if HasErrors(Validate(out)) {
			// two sides can each add the same generated id; that must be reported
			if len(conflicts) == 0 {
				t.Fatalf("iteration %d: a broken merge with no conflict: %v", iter, Validate(out))
			}
		}
		if len(conflicts) == 0 {
			clean++
			rev, rc := Merge3(base, theirs, mine)
			// a side that leaves a reference pointing nowhere (it renamed something after
			// writing the old name) is ambiguous by itself, so its merges are not compared
			if len(rc) == 0 && danglingRefs(mine) <= danglingRefs(base) && danglingRefs(theirs) <= danglingRefs(base) && itemSet(out) != itemSet(rev) {
				t.Fatalf("iteration %d: the two orders disagree about %s\nmine: %s\ntheirs: %s", iter, differing(out, rev), mustJSON(sideOps[0]), mustJSON(sideOps[1]))
			}
		} else {
			conflicted++
		}
	}
	if clean < 30 || conflicted < 15 {
		t.Fatalf("the generator should reach both outcomes: %d clean, %d conflicted", clean, conflicted)
	}
}

// contentOf is an item's text with its lines sorted: the order of things inside
// an item is whichever side's order the merge keeps, so it is not compared.
func contentOf(d *Doc, it *Item) string {
	var lines []string
	for _, l := range strings.Split(canonicalRefs(d, itemText(it)), "\n") {
		if strings.TrimSpace(l) != "" { // paragraph breaks are layout
			lines = append(lines, l)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func itemSet(d *Doc) string {
	_, items, _ := indexDoc(d)
	var texts []string
	for id, i := range items {
		texts = append(texts, id+"\n"+contentOf(d, i.it))
	}
	sort.Strings(texts)
	return strings.Join(texts, "\n--\n")
}

// differing names the items two documents disagree about, with both versions.
func differing(a, b *Doc) string {
	_, ai, _ := indexDoc(a)
	_, bi, _ := indexDoc(b)
	var sb strings.Builder
	for id, x := range ai {
		y, ok := bi[id]
		switch {
		case !ok:
			sb.WriteString("\n[" + id + " only in the first]")
		case contentOf(a, x.it) != contentOf(b, y.it):
			sb.WriteString("\n[" + id + "]\n" + itemText(x.it) + "vs\n" + itemText(y.it))
		}
	}
	for id := range bi {
		if _, ok := ai[id]; !ok {
			sb.WriteString("\n[" + id + " only in the second]")
		}
	}
	return sb.String()
}

// canonicalRefs spells every reference by the id it names, and every reference
// that names nothing the same way ("?"): how a dangling reference was spelled
// depends on which side wrote it, which is not what the merge is checked for.
func canonicalRefs(d *Doc, s string) string {
	return rewriteRefs(s, func(inner string) (string, bool) {
		r := Ref{Inner: inner}
		if strings.HasPrefix(inner, "#") {
			r = Ref{Inner: strings.TrimSpace(inner[1:]), ByID: true}
		}
		if ids := d.refMatches(r); len(ids) == 1 {
			return "#" + ids[0], true
		}
		return "?", true
	})
}

func danglingRefs(d *Doc) int {
	n := 0
	for _, dg := range Validate(d) {
		if dg.Code == "unresolved-ref" {
			n++
		}
	}
	return n
}

// two sides can each add something with the same id; the merge keeps both and
// gives the later one a new id rather than leaving a broken document
func TestMergeGivesCollidingIDsNewOnes(t *testing.T) {
	base := grocery(t)
	mine := mustApply(t, base, PatchOp{Op: "add", Parent: "recurring-orders", List: "done when", Text: "Mine.", ID: "c-same"})
	theirs := mustApply(t, base, PatchOp{Op: "add", Parent: "local-payments", List: "done when", Text: "Theirs.", ID: "c-same"})
	out, conflicts := Merge3(base, mine, theirs)
	if HasErrors(Validate(out)) {
		t.Fatalf("the merge is broken: %v (conflicts %v)", Validate(out), fieldsOfConflicts(conflicts))
	}
	text := Print(out)
	if !strings.Contains(text, "Mine.") || !strings.Contains(text, "Theirs.") {
		t.Fatalf("something was lost:\n%s", text)
	}
}

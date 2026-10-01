package spec

import (
	"fmt"
	"strings"
	"testing"
)

func summarize(cs []Change) []string {
	var out []string
	for _, c := range cs {
		sem := "prose"
		if c.Semantic {
			sem = "semantic"
		}
		out = append(out, fmt.Sprintf("%s %s %s %s %s", c.Op, c.ID, c.Field, sem, c.Kind))
	}
	return out
}

func edit(t *testing.T, d *Doc, old, new string) *Doc {
	t.Helper()
	text := Print(d)
	if !strings.Contains(text, old) {
		t.Fatalf("%q is not in the document", old)
	}
	e, _ := Parse(strings.Replace(text, old, new, 1))
	return e
}

func TestDiffOfIdenticalDocumentsIsEmpty(t *testing.T) {
	d := grocery(t)
	if got := Diff(d, d.Clone()); len(got) != 0 {
		t.Fatalf("%v", summarize(got))
	}
}

func TestWordingChangesAreProseOnlyAndValueChangesAreSemantic(t *testing.T) {
	d := grocery(t)
	cases := []struct {
		name, old, new string
		want           []string
	}{
		{"a description", "Households pay with a card or bank transfer through Paystack.", "Households pay by card or transfer, through Paystack.",
			[]string{"text local-payments description prose feature"}},
		{"the summary", "> Households choose how often they buy items", "> Households pick how often they buy items", []string{"text  summary prose document"}},
		{"a rule: 1 to 8 weeks becomes 1 to 12", "1 to 8 weeks", "1 to 12 weeks", []string{"changed c-91ab text semantic must"}},
		{"a criterion", "- [ ] A failed payment retries and then pauses the order.", "- [ ] A failed payment retries twice and then pauses the order.",
			[]string{"changed c-52c9 text semantic criterion"}},
		{"a property", "priority: must\nuses", "priority: could\nuses", []string{"changed recurring-orders priority semantic feature"}},
		{"a setting", "currency: NGN", "currency: USD", []string{"changed  currency semantic setting"}},
		{"a user's description", "A family that buys groceries every week or month.", "A family that shops every week.",
			[]string{"text household text prose user"}},
		{"a limit (its text is its content)", "Pages open in under 2 seconds", "Pages open in under 1 second", []string{"changed c-6a12 text semantic limit"}},
		{"an entity field", "- quantity: number", "- quantity: whole number", []string{"changed order-line fields semantic entity"}},
		{"an answer", "answer: a", "answer: b", []string{"changed q-auto-charge answer semantic question"}},
		{"a question option", "- (b) No, ask the household to confirm each time", "- (b) No, ask each time", []string{"changed q-auto-charge options semantic question"}},
	}
	for _, c := range cases {
		got := summarize(Diff(d, edit(t, d, c.old, c.new)))
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s:\n got %v\nwant %v", c.name, got, c.want)
		}
	}
}

func TestRenamingIsOneChangeNotOneForEveryReference(t *testing.T) {
	d := grocery(t)
	renamed := mustApply(t, d, PatchOp{Op: "rename", Item: "household", Title: "Family"})
	got := summarize(Diff(d, renamed))
	if strings.Join(got, "|") != "renamed household title semantic user" {
		t.Fatalf("%v", got)
	}
	c := Diff(d, renamed)[0]
	if c.From != "Household" || c.To != "Family" {
		t.Fatalf("%+v", c)
	}
}

func TestAddedRemovedAndMovedThings(t *testing.T) {
	d := grocery(t)
	out := mustApply(t, d,
		PatchOp{Op: "add", Parent: "limits", Text: "cost: cheap"},
		PatchOp{Op: "remove", Item: "product"},
		PatchOp{Op: "add", Parent: "local-payments", List: "done when", Text: "A refund works.", ID: "c-refund"},
		PatchOp{Op: "remove", Item: "c-b310"},
		PatchOp{Op: "move", Item: "c-52c9", Parent: "local-payments"},
	)
	got := strings.Join(summarize(Diff(d, out)), "|")
	for _, want := range []string{
		"added c-refund  semantic criterion", "removed c-b310  semantic criterion", "moved c-52c9  semantic criterion",
		"removed product  semantic entity",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if !strings.Contains(got, "added c-") || strings.Count(got, "limit") != 1 {
		t.Errorf("the new limit: %s", got)
	}
	// an item that moves between two note sections
	a, _ := Parse("## Ideas\n### Idea {#idea}\nx\n\n## Later\n")
	b, _ := Parse("## Ideas\n\n## Later\n### Idea {#idea}\nx\n")
	if got := strings.Join(summarize(Diff(a, b)), "|"); got != "moved idea section semantic note" {
		t.Errorf("moved: %s", got)
	}
}

func TestSemanticKeepsOnlyChangesThatNeedWork(t *testing.T) {
	d := grocery(t)
	e := edit(t, edit(t, d, "through Paystack.", "via Paystack."), "1 to 8 weeks", "1 to 9 weeks")
	all := Diff(d, e)
	sem := Semantic(all)
	if len(all) != 2 || len(sem) != 1 || sem[0].ID != "c-91ab" {
		t.Fatalf("%v / %v", summarize(all), summarize(sem))
	}
	// parse then print is invisible to a diff
	p, _ := Parse(Print(d))
	if len(Diff(d, p)) != 0 {
		t.Fatal("reformatting changed something")
	}
}

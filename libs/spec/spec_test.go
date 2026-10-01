package spec

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current output")

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func files(t *testing.T, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil || len(m) == 0 {
		t.Fatalf("no files match %s", pattern)
	}
	return m
}

func codes(diags []Diagnostic) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = fmt.Sprintf("%d:%d %s", d.Line, d.Col, d.Code)
	}
	return out
}

// the valid corpus is canonical already: printing what was read gives the same
// bytes, and nothing needed a diagnostic
func TestValidCorpusIsAFixedPoint(t *testing.T) {
	for _, f := range files(t, "testdata/valid/*.ospec") {
		src := read(t, f)
		d, diags := Parse(src)
		if got := Print(d); got != src {
			t.Errorf("%s: Print(Parse(x)) differs from x:\n--- want\n%s\n--- got\n%s", f, src, got)
		}
		for _, dg := range diags {
			if dg.Severity != Info {
				t.Errorf("%s: unexpected diagnostic %s", f, dg)
			}
		}
	}
}

// messy human edits print to a reviewed canonical form, and that form is stable
func TestMessyCorpusGolden(t *testing.T) {
	for _, f := range files(t, "testdata/messy/*.ospec") {
		d, _ := Parse(read(t, f))
		got := Print(d)
		golden := strings.TrimSuffix(f, ".ospec") + ".golden"
		if *update {
			if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if want := read(t, golden); got != want {
			t.Errorf("%s:\n--- want\n%s\n--- got\n%s", f, want, got)
		}
		d2, _ := Parse(got)
		if again := Print(d2); again != got {
			t.Errorf("%s: printing is not stable:\n--- first\n%s\n--- second\n%s", f, got, again)
		}
	}
}

// invalid files are read anyway, and say plainly what is wrong and where
func TestInvalidCorpusDiagnostics(t *testing.T) {
	for _, f := range files(t, "testdata/invalid/*.ospec") {
		_, diags := Parse(read(t, f))
		got := strings.Join(codes(diags), "\n") + "\n"
		golden := strings.TrimSuffix(f, ".ospec") + ".diag"
		if *update {
			if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if want := read(t, golden); got != want {
			t.Errorf("%s diagnostics:\n--- want\n%s--- got\n%s", f, want, got)
		}
		for _, dg := range diags {
			if dg.Message == "" || strings.Contains(dg.Message, "%!") {
				t.Errorf("%s: diagnostic without a usable message: %#v", f, dg)
			}
		}
	}
}

// Appendix E.6: the reference example has 23 addressable items
func TestReferenceExampleHasTwentyThreeItems(t *testing.T) {
	d, _ := Parse(read(t, "testdata/valid/grocery.ospec"))
	got := d.Addressables()
	if len(got) != 23 {
		t.Fatalf("%d addressable items, want 23", len(got))
	}
	byKind := map[string]int{}
	for _, a := range got {
		byKind[a.Kind]++
	}
	if byKind["feature"] != 2 || byKind["entity"] != 3 || byKind["criterion"] != 4 || byKind["must"] != 3 || byKind["question"] != 1 {
		t.Fatalf("kinds = %v", byKind)
	}
	if id, ok := d.Resolve("recurring ORDERS"); !ok || id != "recurring-orders" {
		t.Fatalf("Resolve by title = %q, %v", id, ok)
	}
	if id, ok := d.Resolve("#paystack"); !ok || id != "paystack" {
		t.Fatalf("Resolve by id = %q, %v", id, ok)
	}
	if _, ok := d.Resolve("Nothing like it"); ok {
		t.Fatal("an unknown title resolved")
	}
}

func TestParseReadsTheStructure(t *testing.T) {
	d, _ := Parse(read(t, "testdata/valid/grocery.ospec"))
	if d.Version != "1" || d.Title != "Grocery Restocking" || !strings.HasPrefix(d.Summary, "Households choose") {
		t.Fatalf("header: %q %q %q", d.Version, d.Title, d.Summary)
	}
	if len(d.Settings) != 4 || d.Settings[2].Key != "currency" || d.Settings[2].Value != "NGN" {
		t.Fatalf("settings: %v", d.Settings)
	}
	var feat *Item
	for _, s := range d.Sections {
		if s.Name == "Features" {
			feat = s.Nodes[0].(*Item)
		}
	}
	if feat == nil || feat.Title != "Recurring orders" || feat.ID != "recurring-orders" || feat.Kind != "feature" {
		t.Fatalf("first feature: %+v", feat)
	}
	var props, lists int
	for _, n := range feat.Body {
		switch n := n.(type) {
		case *Prop:
			props++
		case *ListProp:
			lists++
			if n.Key == "done when" && (len(n.Entries) != 3 || n.Entries[0].Box != BoxOpen || n.Entries[0].ID != "c-3f2a") {
				t.Fatalf("criteria: %+v", n.Entries)
			}
		}
	}
	if props != 3 || lists != 2 { // for, priority, uses; must, done when
		t.Fatalf("props %d lists %d", props, lists)
	}
	for _, s := range d.Sections {
		if s.Name == "Questions" {
			q := s.Nodes[0].(*Item)
			if q.Style != StyleQuestion || len(q.Options) != 2 || q.Answer != "a" || q.ID != "q-auto-charge" {
				t.Fatalf("question: %+v", q)
			}
		}
	}
}

func TestIDsMayFollowTheNameOrTheText(t *testing.T) {
	for _, in := range []string{
		"## Users\n- **Ada** {#ada}: A person.\n",
		"## Users\n- **Ada**: A person. {#ada}\n",
	} {
		d, _ := Parse(in)
		it := d.Sections[0].Nodes[0].(*Item)
		if it.Title != "Ada" || it.Text != "A person." || it.ID != "ada" || !it.Named {
			t.Errorf("%q → %+v", in, it)
		}
		if got := Print(d); got != "## Users\n- **Ada** {#ada}: A person.\n" {
			t.Errorf("canonical form of %q = %q", in, got)
		}
	}
}

func TestAnIDMarkerMustStandApartFromTheWordBeforeIt(t *testing.T) {
	d, _ := Parse("## Features\n### Thing{#id}\n")
	it := d.Sections[0].Nodes[0].(*Item)
	if it.ID != "" || it.Title != "Thing{#id}" {
		t.Fatalf("%+v", it)
	}
}

func TestBoldWithoutAColonIsJustText(t *testing.T) {
	d, _ := Parse("## Users\n- **Ada** and Bo {#ab}\n")
	it := d.Sections[0].Nodes[0].(*Item)
	if it.Named || it.Text != "**Ada** and Bo" || it.ID != "ab" {
		t.Fatalf("%+v", it)
	}
}

func TestUnknownTextIsKeptNotDropped(t *testing.T) {
	d, diags := Parse("ospec: 1\n# T\n## Features\nstray words\n### F\n#### deeper\n")
	var raw *Raw
	for _, n := range d.Sections[0].Nodes {
		if r, ok := n.(*Raw); ok {
			raw = r
		}
	}
	if raw == nil || raw.Lines[0] != "stray words" {
		t.Fatalf("stray text not kept: %+v", d.Sections[0].Nodes)
	}
	if !strings.Contains(Print(d), "stray words") || !strings.Contains(Print(d), "#### deeper") {
		t.Fatalf("print dropped text:\n%s", Print(d))
	}
	if got := strings.Join(codes(diags), ","); got != "4:1 raw-text" {
		t.Fatalf("diagnostics: %s", got)
	}
}

func TestDiagnosticsOfferASafeFix(t *testing.T) {
	_, diags := Parse("# T\n## Features\n### X {#oops\n")
	var unclosed, noVersion *Diagnostic
	for i := range diags {
		switch diags[i].Code {
		case "id-unclosed":
			unclosed = &diags[i]
		case "no-version":
			noVersion = &diags[i]
		}
	}
	if unclosed == nil || unclosed.Fix == nil || unclosed.Fix.Text != "}" || unclosed.Fix.Line != 3 || unclosed.Fix.Col != len("### X {#oops")+1 {
		t.Fatalf("unclosed id: %+v", unclosed)
	}
	if noVersion == nil || noVersion.Fix == nil || noVersion.Fix.Text != "ospec: 1\n" {
		t.Fatalf("missing version: %+v", noVersion)
	}
}

func TestPrintKeepsEveryWord(t *testing.T) {
	inputs := []string{"", "x", "# T", "## Features\n- a\n  b\n", "* x\n+ y\n", "ospec: 1\nospec: 2\n# A\n# B\n", "## Data\n### E\n- f: text — n\n\n  - nested\n  more\n"}
	for _, f := range files(t, "testdata/*/*.ospec") {
		inputs = append(inputs, read(t, f))
	}
	for _, in := range inputs {
		d, _ := Parse(in)
		out := Print(d)
		if a, b := words(in), words(out); a != b {
			t.Errorf("words changed:\n in: %q\nout: %q\n--- input\n%s\n--- output\n%s", a, b, in, out)
		}
		d2, _ := Parse(out)
		if again := Print(d2); again != out {
			t.Errorf("not stable:\n--- first\n%s\n--- second\n%s", out, again)
		}
	}
}

// words is every letter and digit of s, lowercased and sorted: what must
// survive printing. (The header is printed in a fixed order, so position is not
// compared.)
func words(s string) string {
	var rs []rune
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			rs = append(rs, unicode.ToLower(r))
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
	return string(rs)
}

func TestCRLFAndBOMAreNormalised(t *testing.T) {
	d, _ := Parse("\uFEFF# T\r\n## Users\r\n- **A**: b\r\n")
	if got := Print(d); got != "# T\n\n## Users\n- **A**: b\n" {
		t.Fatalf("%q", got)
	}
}

// ---- Canonicalize ----

func canon(t *testing.T, in string, base *Doc) (*Doc, []Fix) {
	t.Helper()
	d, _ := Parse(in)
	return d, Canonicalize(d, base)
}

func TestCanonicalizeAssignsIDs(t *testing.T) {
	d, fixes := canon(t, `# Shop
## Users
- **Store admin**: Keeps prices.
## Features
### Recurring orders
done when:
- [ ] An order appears.
- [ ] Skipping removes it.
must:
- Repeat weekly.
## Limits
- speed: fast
## Questions
? Should it charge automatically?
- (a) Yes
## Decisions
- 2026-09-21 — Failed payments retry 3 times. (@me)
## Assumptions
- Deliveries happen Monday to Saturday.
`, nil)
	if d.Version != "1" {
		t.Fatalf("version %q", d.Version)
	}
	got := map[string]bool{}
	for _, a := range d.Addressables() {
		got[a.ID] = true
	}
	for _, want := range []string{"store-admin", "recurring-orders", "q-should-it-charge-automatically", "d-2026-09-21-failed-payments", "a-deliveries-happen-monday-to-saturday"} {
		if !got[want] {
			t.Errorf("no id %q in %v", want, got)
		}
	}
	// users 1, features 1, limits 1, questions 1, decisions 1, assumptions 1 = 6 items; 2 criteria; 1 rule
	if n := len(d.Addressables()); n != 9 {
		t.Errorf("%d ids, want 9", n)
	}
	for _, a := range d.Addressables() {
		if a.Kind == "limit" && !strings.HasPrefix(a.ID, "c-") {
			t.Errorf("an anonymous item got %q, want c-xxxx", a.ID)
		}
	}
	if len(fixes) != 9 {
		t.Errorf("%d fixes reported, want 9", len(fixes))
	}
	// no id is shared
	seen := map[string]bool{}
	for _, a := range d.Addressables() {
		if seen[a.ID] {
			t.Errorf("id %q appears twice", a.ID)
		}
		seen[a.ID] = true
	}
	// and the printed result reads back with the same ids, and canonicalizing again changes nothing
	out := Print(d)
	d2, _ := Parse(out)
	if fx := Canonicalize(d2, nil); len(fx) != 0 || Print(d2) != out {
		t.Fatalf("not idempotent: %v\n%s", fx, Print(d2))
	}
}

func TestCanonicalizeKeepsExistingIDsAndMakesNewOnesUnique(t *testing.T) {
	d, _ := canon(t, "## Features\n### Orders {#orders}\n### Orders\n### Orders\n", nil)
	var ids []string
	for _, a := range d.Addressables() {
		ids = append(ids, a.ID)
	}
	if strings.Join(ids, ",") != "orders,orders-2,orders-3" {
		t.Fatalf("%v", ids)
	}
}

func TestCanonicalizeReIDsAPastedDuplicate(t *testing.T) {
	d, fixes := canon(t, "## Features\n### Orders {#orders}\nfirst\n### Orders copy {#orders}\nsecond\n", nil)
	items := d.Sections[0].Nodes
	if items[0].(*Item).ID != "orders" || items[1].(*Item).ID != "orders-copy" {
		t.Fatalf("%q %q", items[0].(*Item).ID, items[1].(*Item).ID)
	}
	if len(fixes) != 1 || !strings.Contains(fixes[0].Message, "used twice") || !strings.Contains(fixes[0].Message, "orders-copy") {
		t.Fatalf("%v", fixes)
	}
}

func TestRenameKeepsTheIDAndRewritesReferences(t *testing.T) {
	base, _ := canon(t, `## Users
- **Household** {#household}: A family.
## Features
### Recurring orders {#recurring-orders}
for: [[Household]]
uses: [[household]] and [[#household]] and [[Other thing]]
done when:
- [ ] A [[Household ]] sets it up. {#c-1111}
`, nil)
	edited, _ := Parse(strings.Replace(Print(base), "**Household**", "**Family**", 1))
	fixes := Canonicalize(edited, base)
	out := Print(edited)
	for _, want := range []string{"for: [[Family]]", "uses: [[Family]] and [[#household]] and [[Other thing]]", "A [[Family]] sets it up."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "[[Household") {
		t.Errorf("an old reference is left:\n%s", out)
	}
	if len(fixes) != 1 || !strings.Contains(fixes[0].Message, "Family") {
		t.Fatalf("%v", fixes)
	}
	if id, ok := edited.Resolve("Family"); !ok || id != "household" {
		t.Fatalf("the renamed item changed its id: %q %v", id, ok)
	}
}

func TestRenameLeavesAReferenceAloneWhenAnotherItemNowUsesTheOldTitle(t *testing.T) {
	base, _ := canon(t, "## Users\n- **A** {#a}: x\n- **B** {#b}: y\n## Notes\nsee [[A]]\n", nil)
	edited, _ := Parse("## Users\n- **B** {#a}: x\n- **A** {#b}: y\n## Notes\nsee [[A]]\n")
	Canonicalize(edited, base)
	if !strings.Contains(Print(edited), "see [[A]]") {
		t.Fatalf("the ambiguous reference was rewritten:\n%s", Print(edited))
	}
}

func TestReferencesScansBothForms(t *testing.T) {
	got := References("a [[One]] b [[#two]] c [[ Three ]] [[]] [[unclosed")
	want := []Ref{{Inner: "One"}, {Inner: "two", ByID: true}, {Inner: "Three"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%v", got)
	}
}

// ---- fuzzing ----

func FuzzParsePrint(f *testing.F) {
	for _, g := range []string{"testdata/*/*.ospec"} {
		m, _ := filepath.Glob(g)
		for _, p := range m {
			if b, err := os.ReadFile(p); err == nil {
				f.Add(string(b))
			}
		}
	}
	f.Add("")
	f.Add("## Features\n### A\nkey:\n- x\n  - y\nz\n")
	f.Add("? q {#id}\n- (a) b\nanswer: a\n")
	f.Fuzz(func(t *testing.T, in string) {
		d, _ := Parse(in)
		out := Print(d)
		if a, b := words(in), words(out); a != b {
			t.Fatalf("words changed\n in: %q\nout: %q\ninput: %q\noutput: %q", a, b, in, out)
		}
		d2, _ := Parse(out)
		if again := Print(d2); again != out {
			t.Fatalf("not stable\ninput: %q\nfirst: %q\nsecond: %q", in, out, again)
		}
		// canonicalizing must not panic, must keep ids unique, and must be a fixed point
		Canonicalize(d, nil)
		seen := map[string]bool{}
		for _, a := range d.Addressables() {
			if seen[a.ID] {
				t.Fatalf("duplicate id %q after Canonicalize of %q", a.ID, in)
			}
			seen[a.ID] = true
		}
		cp := Print(d)
		d3, _ := Parse(cp)
		if fx := Canonicalize(d3, nil); len(fx) != 0 || Print(d3) != cp {
			t.Fatalf("Canonicalize not a fixed point for %q: %v", in, fx)
		}
	})
}

// shape is the kinds of nodes in an item's body, in order
func shape(it *Item) string {
	var sb strings.Builder
	for _, n := range it.Body {
		switch n.(type) {
		case *Para:
			sb.WriteString("P")
		case *Prop:
			sb.WriteString("p")
		case *ListProp:
			sb.WriteString("L")
		case *Child:
			sb.WriteString("C")
		default:
			sb.WriteString("?")
		}
	}
	return sb.String()
}

// what one part of an item looks like next to another must survive printing and
// reading again: a bullet after a list is not one of its entries, text after a
// bullet is not its continuation, two paragraphs stay two
func TestPrintingKeepsNeighbouringPartsApart(t *testing.T) {
	cases := map[string]string{
		"a list, then a bullet":        "## Features\n\n### A\nmust:\n- one\n\n- two\n",
		"an empty key, then a bullet":  "## Features\n\n### A\nkey:\n\n- child\n",
		"a bullet, then text":          "## Features\n\n### A\n- x\n\n  indented text\n",
		"two paragraphs":               "## Features\n\n### A\nfirst\n\nsecond\n",
		"text, then a property":        "## Features\n\n### A\nwords\nkey: value\n",
		"a nested bullet after a list": "## Features\n\n### A\nmust:\n- one\n\n  - nested\n",
	}
	for name, in := range cases {
		d, _ := Parse(in)
		before := shape(d.Sections[0].Nodes[0].(*Item))
		out := Print(d)
		d2, _ := Parse(out)
		if after := shape(d2.Sections[0].Nodes[0].(*Item)); after != before {
			t.Errorf("%s: shape %q became %q\n--- printed\n%s", name, before, after, out)
		}
		if out != in {
			t.Errorf("%s: not canonical as written:\n--- want\n%s--- got\n%s", name, in, out)
		}
	}
	// and a bullet item under a question is not read as an option
	d, _ := Parse("## Questions\n? Why?\n\n- (a) not an option, a bullet item\n")
	if q := d.Sections[0].Nodes[0].(*Item); len(q.Options) != 0 {
		t.Fatalf("read as an option: %+v", q)
	}
	d2, _ := Parse(Print(d))
	if q := d2.Sections[0].Nodes[0].(*Item); len(q.Options) != 0 || len(d2.Sections[0].Nodes) != 2 {
		t.Fatalf("after printing: %+v", d2.Sections[0].Nodes)
	}
}

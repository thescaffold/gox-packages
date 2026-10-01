package spec

import (
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/thescaffold/gox-packages/libs/design"
)

func compile(t *testing.T, src string) (*design.Graph, []Diagnostic) {
	t.Helper()
	d, _ := Parse(src)
	g, diags := Compile(d)
	if problems := g.Validate(); len(problems) != 0 {
		t.Fatalf("the compiled graph does not hold together: %v\n%s", problems, src)
	}
	return g, diags
}

func edgeSet(g *design.Graph) []string {
	var out []string
	for _, e := range g.Edges {
		out = append(out, e.From+" "+string(e.Kind)+" "+e.To)
	}
	sort.Strings(out)
	return out
}

func kinds(g *design.Graph, k design.NodeKind) string {
	var ids []string
	for _, n := range g.Of(k) {
		ids = append(ids, n.ID)
	}
	return strings.Join(ids, ",")
}

// PLAN M2-01c: the E.6 example compiles to actors, capabilities, entities, an
// interface, an integration and two environments, joined as the spec says
func TestTheReferenceExampleCompilesToTheExpectedGraph(t *testing.T) {
	d := grocery(t)
	g, diags := Compile(d)
	if len(diags) != 0 {
		t.Fatalf("%v", diags)
	}
	for kind, want := range map[design.NodeKind]string{
		design.Actor:       "household,store-admin",
		design.Capability:  "recurring-orders,local-payments",
		design.Entity:      "order,order-line,product",
		design.Interface:   "my-orders",
		design.Integration: "paystack",
		design.Environment: "staging,production",
		design.Service:     "",
	} {
		if got := kinds(g, kind); got != want {
			t.Errorf("%s nodes: %q, want %q", kind, got, want)
		}
	}
	wantEdges := []string{
		"household uses local-payments", "household uses my-orders", "household uses recurring-orders",
		"local-payments uses paystack",
		"my-orders deploys_to production", "my-orders deploys_to staging", "my-orders uses order",
		"order uses household", "order uses order-line",
		"order-line uses product",
		"recurring-orders uses order", "recurring-orders uses product",
	}
	if got := edgeSet(g); strings.Join(got, "\n") != strings.Join(wantEdges, "\n") {
		t.Errorf("edges:\n got %v\nwant %v", got, wantEdges)
	}

	rec := g.Node("recurring-orders")
	if rec.Priority != "must" || len(rec.Rules) != 3 || len(rec.Criteria) != 3 || rec.Criteria[0].ID != "c-3f2a" || rec.Rules[2].ID != "c-77f1" {
		t.Errorf("capability: %+v", rec)
	}
	if rec.Inferred || g.Node("household").Inferred {
		t.Error("nothing in the reference example is inferred")
	}
	order := g.Node("order")
	if len(order.Fields) != 4 || order.Fields[0].Ref != "household" || order.Fields[1].Ref != "order-line" || !order.Fields[1].Many ||
		order.Fields[2].Type != "one of" || strings.Join(order.Fields[2].Options, ",") != "waiting,paid,delivered,cancelled" ||
		order.Fields[3].Type != "yes/no" || order.Fields[3].Note != "set when the order came from a schedule" {
		t.Errorf("entity fields: %+v", order.Fields)
	}
	my := g.Node("my-orders")
	if my.Device != "both" || strings.Join(my.Actions, "|") != "skip next order|change how often|pause" {
		t.Errorf("interface: %+v", my)
	}
	pay := g.Node("paystack")
	if strings.Join(pay.Keys, ",") != "PAYSTACK_SECRET,PAYSTACK_WEBHOOK_SECRET" || pay.Summary != "Takes card and transfer payments" {
		t.Errorf("integration: %+v", pay)
	}
	// both environments declare the integration's keys, with no values anywhere; production adds its own
	if got := strings.Join(g.Node("staging").Keys, ","); got != "PAYSTACK_SECRET,PAYSTACK_WEBHOOK_SECRET" {
		t.Errorf("staging keys: %s", got)
	}
	prod := g.Node("production")
	if got := strings.Join(prod.Keys, ","); got != "PAYSTACK_SECRET,PAYSTACK_WEBHOOK_SECRET,SMTP_HOST" || prod.Protected == nil || !*prod.Protected {
		t.Errorf("production: %+v", prod)
	}
	if g.Node("staging").Protected != nil {
		t.Error("staging never said whether it is protected")
	}
	if len(g.Limits) != 2 || g.Limits[0].Kind != "speed" || g.Limits[1].Kind != "privacy" {
		t.Errorf("limits: %+v", g.Limits)
	}
	// every edge says where in the spec it came from
	for _, e := range g.Edges {
		if _, ok := d.Resolve("#" + e.Source); !ok {
			t.Errorf("edge %v has no source item", e)
		}
	}

	// the whole graph, reviewed, is the golden
	b, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	golden := "testdata/design/grocery.design.json"
	if *update {
		if err := os.WriteFile(golden, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if want := read(t, golden); string(b)+"\n" != want {
		t.Errorf("the compiled graph differs from %s:\n%s", golden, b)
	}
	if back, err := design.Parse(b); err != nil || len(back.Nodes) != len(g.Nodes) {
		t.Errorf("the graph does not read back: %v", err)
	}
}

func TestNodeIDsAreTheSpecItemIDsAndSurviveARename(t *testing.T) {
	d := grocery(t)
	before, _ := Compile(d)
	renamed := mustApply(t, d, PatchOp{Op: "rename", Item: "household", Title: "Family"}, PatchOp{Op: "rename", Item: "recurring-orders", Title: "Repeat orders"})
	after, _ := Compile(renamed)
	if g := edgeSet(before); strings.Join(g, "|") != strings.Join(edgeSet(after), "|") {
		t.Fatalf("a rename changed the edges:\n%v\n%v", g, edgeSet(after))
	}
	if after.Node("household").Name != "Family" || after.Node("recurring-orders").Name != "Repeat orders" || before.Node("household").Name != "Household" {
		t.Fatal("the names should follow the spec")
	}
	if len(Diff2(before, after)) != 0 {
		t.Fatalf("only names differ")
	}
}

// Diff2 lists ids present in one graph and not the other.
func Diff2(a, b *design.Graph) []string {
	var out []string
	for _, n := range a.Nodes {
		if b.Node(n.ID) == nil {
			out = append(out, "-"+n.ID)
		}
	}
	for _, n := range b.Nodes {
		if a.Node(n.ID) == nil {
			out = append(out, "+"+n.ID)
		}
	}
	return out
}

func TestPartsBecomeServicesJobsAndInterfaces(t *testing.T) {
	g, diags := compile(t, `ospec: 1
# Parts

## Features

### Orders {#orders}
for: [[Shopper]]

### Reminders {#reminders}

## Users
- **Shopper** {#shopper}: Buys.

## Data

### Order {#order}
- note: text

## Integrations
- **Mailer** {#mailer}: Sends mail. needs keys: MAIL_KEY

## Parts

### Shop {#shop}
does: [[Orders]]
owns: [[Order]]
talks to: [[Mailer]], [[Cleanup]]

### Cleanup {#cleanup}
runs as: job
does: [[Reminders]]

### Public API {#api}
runs as: interface
does: [[Orders]]

## Environments

### live {#live}
protected: yes
`)
	if len(diags) != 0 {
		t.Fatalf("%v", diags)
	}
	if kinds(g, design.Service) != "shop" || kinds(g, design.Job) != "cleanup" || kinds(g, design.Interface) != "api" {
		t.Fatalf("service %q job %q interface %q", kinds(g, design.Service), kinds(g, design.Job), kinds(g, design.Interface))
	}
	want := []string{
		"api deploys_to live", "api owns orders", "cleanup deploys_to live", "cleanup owns reminders",
		"shop calls cleanup", "shop calls mailer", "shop deploys_to live", "shop owns order", "shop owns orders",
		"shopper uses orders",
	}
	if got := edgeSet(g); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("edges:\n got %v\nwant %v", got, want)
	}
}

func TestInferredItemsAreMarkedAndSoIsWhatComesFromThem(t *testing.T) {
	g, _ := compile(t, `ospec: 1
# T

## Users
- **Guest** {#guest}: Someone passing. (inferred by Origine)
- **Owner** {#owner}: The boss.

## Features

### Browse {#browse}
for: [[Guest]], [[Owner]]
inferred: yes

### Sell {#sell}
for: [[Owner]]
`)
	if !g.Node("guest").Inferred || g.Node("owner").Inferred || !g.Node("browse").Inferred || g.Node("sell").Inferred {
		t.Fatalf("inferred flags: %+v", g.Nodes)
	}
	if g.Node("guest").Summary != "Someone passing." {
		t.Fatalf("the marker is not part of the description: %q", g.Node("guest").Summary)
	}
	for _, e := range g.Edges {
		if want := e.Source == "browse"; e.Inferred != want {
			t.Errorf("edge %v inferred=%v, want %v", e, e.Inferred, want)
		}
	}
}

func TestRelationsThatCannotBeDrawnAreLeftOutAndSaidSo(t *testing.T) {
	g, diags := compile(t, `ospec: 1
# T

## Users
- **Shopper** {#shopper}: Buys.

## Features

### Orders {#orders}
for: [[Shopper]], [[Nobody]]
uses: [[Orders]], [[Shopper]], [[#c-9999]]
needs: [[Twin]]

### Twin {#twin}

### Twin {#twin-2}

## Data

### Order {#order}
- owner: [[Missing]]
`)
	got := map[string]int{}
	for _, d := range diags {
		got[d.Code]++
	}
	for code, n := range map[string]int{"design-unresolved-ref": 3, "design-ambiguous-ref": 1, "design-bad-relation": 1} {
		if got[code] != n {
			t.Errorf("%s: %d, want %d (%v)", code, got[code], n, codesOf(diags))
		}
	}
	// the one thing that could be drawn is there; nothing else is
	if e := edgeSet(g); strings.Join(e, "|") != "shopper uses orders" {
		t.Fatalf("edges: %v", e)
	}
	for _, d := range diags {
		if d.Line == 0 || d.Severity == Error {
			t.Errorf("a relation problem is a warning with a line: %v", d)
		}
	}
}

func TestAFieldCanOnlyPointAtAnEntityOrAKindOfPerson(t *testing.T) {
	g, diags := compile(t, "ospec: 1\n# T\n## Features\n### Buy {#buy}\n## Users\n- **Shopper** {#shopper}: x\n## Data\n### Order {#order}\n- what: [[Buy]]\n- who: [[Shopper]]\n")
	f := g.Node("order").Fields
	if f[0].Ref != "" || f[1].Ref != "shopper" || codesOf(diags) != "design-bad-relation" {
		t.Fatalf("%+v %v", f, diags)
	}
}

func TestEntityFieldsAndAnItemWithoutAName(t *testing.T) {
	g, diags := compile(t, `ospec: 1
# T

## Data

### Thing {#thing}
- name
- size: Number — in centimetres
- tags: many text
- kind: one of big, small
- weird: something odd — unusual

## Users
- no name here {#anon}
`)
	f := g.Node("thing").Fields
	if len(f) != 5 || f[0].Type != "text" || f[1].Type != "number" || f[1].Note != "in centimetres" || !f[2].Many || f[2].Type != "text" ||
		f[3].Type != "one of" || len(f[3].Options) != 2 || f[4].Type != "something odd" {
		t.Fatalf("fields: %+v", f)
	}
	if g.Node("anon") != nil || codesOf(diags) != "design-no-name" {
		t.Fatalf("an item with no name is left out and said so: %v", diags)
	}
}

func TestCompileNeverChangesTheDocumentAndGivesItemsIDs(t *testing.T) {
	d, _ := Parse("# T\n## Features\n### Alpha\ndone when:\n- [ ] works\n## Users\n- **Beta**: b\n")
	before := Print(d)
	g, _ := Compile(d)
	if Print(d) != before {
		t.Fatal("Compile changed the document it was given")
	}
	if g.Node("alpha") == nil || g.Node("beta") == nil || g.Node("alpha").Criteria[0].ID == "" {
		t.Fatalf("items without ids should get the ones Canonicalize gives: %+v", g.Nodes)
	}
}

func TestEveryCorpusFileCompilesToAValidGraph(t *testing.T) {
	for _, f := range files(t, "testdata/*/*.ospec") {
		compile(t, read(t, f))
	}
}

func TestRandomPatchesCompileToValidGraphs(t *testing.T) {
	base := grocery(t)
	for i := 0; i < 300; i++ {
		d := base
		for tries := 0; tries < 10; tries++ {
			ops := []PatchOp{randomOp(rngFor(i), d, i, tries)}
			if out, err := Apply(d, SpecPatch{Ops: ops}); err == nil {
				d = out
			}
		}
		g, _ := Compile(d)
		if ps := g.Validate(); len(ps) != 0 {
			t.Fatalf("iteration %d: %v", i, ps)
		}
	}
}

// whatever text arrives, it compiles to a graph that holds together and reads back
func FuzzCompile(f *testing.F) {
	for _, g := range []string{"testdata/*/*.ospec"} {
		m, _ := filepathGlob(g)
		for _, p := range m {
			if b, err := os.ReadFile(p); err == nil {
				f.Add(string(b))
			}
		}
	}
	f.Add("## Parts\n### A\ndoes: [[A]]\nowns: [[A]]\n## Environments\n### e\nneeds keys: X\n")
	f.Fuzz(func(t *testing.T, in string) {
		d, _ := Parse(in)
		g, diags := Compile(d)
		for _, dg := range diags {
			if dg.Code == "design-invalid" {
				t.Fatalf("the graph does not hold together: %s\n%q", dg.Message, in)
			}
		}
		b, err := g.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := design.Parse(b); err != nil {
			t.Fatalf("the graph does not read back: %v\n%q", err, in)
		}
	})
}

func rngFor(i int) *rand.Rand { return rand.New(rand.NewSource(int64(i) + 99)) }

func filepathGlob(pattern string) ([]string, error) { return filepath.Glob(pattern) }

func TestTheSameRelationWrittenTwiceIsDrawnOnce(t *testing.T) {
	g, diags := compile(t, "ospec: 1\n# T\n## Users\n- **Shopper** {#shopper}: x\n## Data\n### Order {#order}\n- a: text\n## Features\n### Buy {#buy}\nfor: [[Shopper]], [[#shopper]]\nuses: [[Order]]\nneeds: [[Order]]\n")
	if len(diags) != 0 || len(g.Edges) != 2 {
		t.Fatalf("%v %v", diags, edgeSet(g))
	}
	if g.Edges[1].Note != "uses" {
		t.Fatalf("the first way it was written is kept: %+v", g.Edges)
	}
}

func TestAValueWhereAKeyNameBelongsStaysOutOfTheDesign(t *testing.T) {
	g, diags := compile(t, "ospec: 1\n# T\n## Integrations\n- **Mailer** {#mailer}: Sends. needs keys: MAIL_KEY, hunter2secret, sk-live-abcdefghijklmnop\n## Environments\n### live {#live}\nneeds keys: DB_URL, my password\n")
	if got := strings.Join(g.Node("mailer").Keys, ","); got != "MAIL_KEY" {
		t.Fatalf("integration keys: %s", got)
	}
	if got := strings.Join(g.Node("live").Keys, ","); got != "DB_URL,MAIL_KEY" {
		t.Fatalf("environment keys: %s", got)
	}
	n := 0
	for _, d := range diags {
		if d.Code == "design-bad-key" {
			n++
			if strings.Contains(d.Message, "hunter2") || strings.Contains(d.Message, "sk-live") || strings.Contains(d.Message, "password") {
				t.Errorf("the warning repeats the value: %s", d.Message)
			}
		}
	}
	if n != 3 {
		t.Fatalf("%d warnings, want one for each thing dropped: %v", n, diags)
	}
	b, _ := g.JSON()
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "sk-live") {
		t.Fatal("the value reached the design JSON")
	}
}

func TestOnlyTheWordsAPropertyAllowsReachTheDesign(t *testing.T) {
	g, diags := compile(t, "ospec: 1\n# T\n## Features\n### A {#a}\npriority: Must\n### B {#b}\npriority: urgent\n## Screens\n### S {#s}\ndevice: watch\n### S2 {#s2}\ndevice: Both\n## Environments\n### e {#e}\nprotected: maybe\n### e2 {#e2}\nprotected: no\n")
	if g.Node("a").Priority != "must" || g.Node("b").Priority != "" || g.Node("s").Device != "" || g.Node("s2").Device != "both" {
		t.Fatalf("%+v", g.Nodes)
	}
	if g.Node("e").Protected != nil || g.Node("e2").Protected == nil || *g.Node("e2").Protected {
		t.Fatalf("protected: %+v %+v", g.Node("e").Protected, g.Node("e2").Protected)
	}
	n := 0
	for _, d := range diags {
		if d.Code == "design-bad-value" {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("%d warnings: %v", n, diags)
	}
}

func TestTheKeysClauseIsFoundWhateverItsCase(t *testing.T) {
	g, _ := compile(t, "ospec: 1\n# T\n## Integrations\n- **Mailer** {#mailer}: Sends mail. Needs Keys: MAIL_KEY\n")
	if m := g.Node("mailer"); strings.Join(m.Keys, ",") != "MAIL_KEY" || m.Summary != "Sends mail" {
		t.Fatalf("%+v", m)
	}
}

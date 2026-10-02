package prototype_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/thescaffold/gox-packages/libs/design"
	"github.com/thescaffold/gox-packages/libs/prototype"
	"github.com/thescaffold/gox-packages/libs/spec"
)

func grocery(t *testing.T) *design.Graph {
	t.Helper()
	b, err := os.ReadFile("../spec/testdata/valid/grocery.ospec")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := spec.Parse(string(b))
	g, _ := spec.Compile(doc)
	if g == nil {
		t.Fatal("no graph")
	}
	return g
}

func TestTheGroceryExampleMakesAPrototypeThatKeepsItsOwnRules(t *testing.T) {
	r, err := prototype.Build(grocery(t), prototype.Options{Revision: 7, BuildID: "b-7"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"index.html", "assets/app.js", "assets/style.css", "assets/data.js", "manifest.json"} {
		if len(r.Files[f]) == 0 {
			t.Errorf("missing %s", f)
		}
	}
	if v := prototype.Check(r.Files); len(v) > 0 {
		t.Fatalf("the kit breaks its own rules: %v", v)
	}
	if r.Manifest.Revision != 7 || r.Manifest.BuildID != "b-7" || r.Manifest.Generator != prototype.Generator || len(r.Manifest.Files) != 4 {
		t.Errorf("manifest: %+v", r.Manifest)
	}
	// the manifest file says the same, and lists the hash of every other file
	var onDisk prototype.Manifest
	if err := json.Unmarshal(r.Files["manifest.json"], &onDisk); err != nil || onDisk.Revision != 7 || len(onDisk.Files) != 4 {
		t.Errorf("manifest.json: %v %+v", err, onDisk)
	}
	again, _ := prototype.Build(grocery(t), prototype.Options{Revision: 7, BuildID: "b-7"})
	for name, b := range r.Files {
		if !bytes.Equal(b, again.Files[name]) {
			t.Errorf("%s differs between two builds of the same design", name)
		}
	}
	total := 0
	for _, b := range r.Files {
		total += len(b)
	}
	if total > 60<<10 {
		t.Errorf("a small prototype should be small: %d bytes", total)
	}
}

func TestTheDataComesFromTheDesign(t *testing.T) {
	r, _ := prototype.Build(grocery(t), prototype.Options{Revision: 1})
	data := string(r.Files["assets/data.js"])
	js := strings.TrimSuffix(strings.TrimPrefix(data[strings.Index(data, "window.PROTO = "):], "window.PROTO = "), ";\n")
	var p struct {
		Title    string
		Revision int
		Actors   []struct{ ID, Name string }
		Screens  []struct {
			ID       string
			Name     string
			Device   string
			Actions  []string
			Actors   []string
			Entities []string
		}
		Entities []struct {
			ID     string
			Name   string
			Fields []struct{ Name, Type, Ref string }
			Rows   []map[string]any
		}
		Features []struct {
			Name     string
			Criteria []struct{ Text string }
		}
	}
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		t.Fatalf("data.js is not the JSON it claims: %v", err)
	}
	if p.Title != "Grocery Restocking" || p.Revision != 1 || len(p.Actors) != 2 {
		t.Errorf("%+v", p)
	}
	var screen *struct {
		ID       string
		Name     string
		Device   string
		Actions  []string
		Actors   []string
		Entities []string
	}
	for i := range p.Screens {
		if p.Screens[i].ID == "my-orders" {
			screen = &p.Screens[i]
		}
	}
	if screen == nil || screen.Device != "both" || len(screen.Actions) != 3 || len(screen.Actors) != 1 || screen.Actors[0] != "household" || len(screen.Entities) != 1 || screen.Entities[0] != "order" {
		t.Fatalf("the My orders screen: %+v", screen)
	}
	ids := map[string]bool{}
	for _, e := range p.Entities {
		if len(e.Rows) != 4 {
			t.Errorf("%s has %d rows", e.ID, len(e.Rows))
		}
		for _, r := range e.Rows {
			ids[r["id"].(string)] = true
		}
	}
	// every reference points at a row that exists
	for _, e := range p.Entities {
		for _, f := range e.Fields {
			if f.Type != "reference" {
				continue
			}
			for _, r := range e.Rows {
				switch v := r[f.Name].(type) {
				case string:
					if !ids[v] && v != "Household" && v != "Store admin" {
						t.Errorf("%s.%s points at %q, which is not a row", e.ID, f.Name, v)
					}
				case []any:
					for _, x := range v {
						if !ids[x.(string)] {
							t.Errorf("%s.%s points at %q, which is not a row", e.ID, f.Name, x)
						}
					}
				}
			}
		}
	}
	if len(p.Features) < 2 || len(p.Features[0].Criteria) == 0 {
		t.Errorf("features: %+v", p.Features)
	}
}

func TestNothingToShowWithoutScreens(t *testing.T) {
	if _, err := prototype.Build(nil, prototype.Options{Revision: 1}); !errors.Is(err, prototype.ErrNothingToShow) {
		t.Errorf("nil: %v", err)
	}
	if _, err := prototype.Build(&design.Graph{Nodes: []design.Node{{ID: "a", Kind: design.Actor, Name: "A"}}}, prototype.Options{Revision: 1}); !errors.Is(err, prototype.ErrNothingToShow) {
		t.Errorf("no screens: %v", err)
	}
	if _, err := prototype.Build(grocery(t), prototype.Options{}); err == nil {
		t.Error("a prototype shows a revision")
	}
}

func TestWhatTheSpecSaysCannotBreakOutOfThePage(t *testing.T) {
	g := &design.Graph{Title: `Shop </title><script>alert(1)</script>`, Nodes: []design.Node{
		{ID: "s", Kind: design.Interface, Name: `</script><script>alert(2)</script>`, Summary: `"><img src=x onerror=alert(3)>`, Actions: []string{`<b>x</b>`}},
	}}
	r, err := prototype.Build(g, prototype.Options{Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	idx := string(r.Files["index.html"])
	if strings.Contains(idx, "<script>alert") || !strings.Contains(idx, "&lt;/title&gt;") {
		t.Errorf("the page title is not escaped: %s", idx)
	}
	// the data is JSON in its own file, read as text by the script, never as markup
	if strings.Contains(string(r.Files["assets/app.js"]), "innerHTML") {
		t.Error("the kit's script writes markup from data")
	}
	if v := prototype.Check(r.Files); len(v) > 0 {
		t.Errorf("%v", v)
	}
}

func kit(t *testing.T) map[string][]byte {
	t.Helper()
	r, err := prototype.Build(grocery(t), prototype.Options{Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for k, v := range r.Files {
		files[k] = append([]byte(nil), v...)
	}
	return files
}

// reseal makes the manifest match the files again, so a test sees only the rule it is about.
func reseal(files map[string][]byte) {
	m := prototype.Manifest{Format: 1, Revision: 2, BuildID: "x", Generator: "test", Files: map[string]string{}}
	for n, b := range files {
		if n != "manifest.json" {
			m.Files[n] = prototype.Sum(b)
		}
	}
	files["manifest.json"], _ = json.Marshal(m)
}

func rules(v []prototype.Violation) string {
	var s []string
	for _, x := range v {
		s = append(s, x.Rule)
	}
	return strings.Join(s, ",")
}

func TestTheCheckerRejectsWhatWouldLetAPrototypeCallOutOrRunAway(t *testing.T) {
	bad := map[string]struct {
		file, add, rule string
	}{
		"module script":      {"index.html", `<script type="module" src="assets/m.js"></script>`, "classic-scripts"},
		"import statement":   {"assets/app.js", "\nimport x from './x.js';\n", "classic-scripts"},
		"dynamic import":     {"assets/app.js", "\nimport('./x.js');\n", "classic-scripts"},
		"fetch":              {"assets/app.js", "\nfetch('/api');\n", "no-network"},
		"xhr":                {"assets/app.js", "\nnew XMLHttpRequest();\n", "no-network"},
		"websocket":          {"assets/app.js", "\nnew WebSocket('wss://x');\n", "no-network"},
		"beacon":             {"assets/app.js", "\nnavigator.sendBeacon('/x');\n", "no-network"},
		"service worker":     {"assets/app.js", "\nnavigator.serviceWorker.register('sw.js');\n", "no-network"},
		"worker":             {"assets/app.js", "\nnew Worker('w.js');\n", "no-network"},
		"eval":               {"assets/app.js", "\neval('1');\n", "no-eval"},
		"new Function":       {"assets/app.js", "\nnew Function('return 1');\n", "no-eval"},
		"document.write":     {"assets/app.js", "\ndocument.write('x');\n", "no-eval"},
		"remote script":      {"index.html", `<script src="https://cdn.example.com/lib.js"></script>`, "relative-urls"},
		"remote stylesheet":  {"index.html", `<link rel="stylesheet" href="//cdn.example.com/a.css">`, "relative-urls"},
		"absolute path":      {"index.html", `<img src="/logo.png">`, "relative-urls"},
		"remote image":       {"index.html", `<img src='http://x.test/a.png'>`, "relative-urls"},
		"remote iframe":      {"index.html", `<iframe src=https://x.test></iframe>`, "relative-urls"},
		"css url":            {"assets/style.css", "\nbody{background:url(https://x.test/a.png)}\n", "no-network"},
		"css absolute url":   {"assets/style.css", "\nbody{background:url('/a.png')}\n", "no-network"},
		"css import":         {"assets/style.css", "\n@import url(https://fonts.example.com/f.css);\n", "no-network"},
		"form to a server":   {"index.html", `<form action="https://x.test/steal"></form>`, "forms"},
		"form to a path":     {"index.html", `<form method=post action=/save></form>`, "forms"},
		"meta refresh":       {"index.html", `<meta http-equiv="refresh" content="0;url=https://x.test">`, "relative-urls"},
		"base tag":           {"index.html", `<base href="https://x.test/">`, "relative-urls"},
		"script in svg":      {"assets/x.svg", `<svg><script>1</script></svg>`, "no-script-in-svg"},
		"file type":          {"assets/run.exe", "MZ", "type"},
		"path traversal":     {"../escape.html", "x", "path"},
		"absolute file name": {"/etc/passwd", "x", "path"},
	}
	for name, c := range bad {
		files := kit(t)
		if strings.HasSuffix(c.file, ".html") && files[c.file] != nil {
			files[c.file] = append(files[c.file], []byte(c.add)...)
		} else if files[c.file] != nil {
			files[c.file] = append(files[c.file], []byte(c.add)...)
		} else {
			files[c.file] = []byte(c.add)
		}
		reseal(files)
		v := prototype.Check(files)
		if !strings.Contains(rules(v), c.rule) {
			t.Errorf("%s: want rule %s, got %q (%v)", name, c.rule, rules(v), v)
		}
	}
}

func TestTheCheckerAllowsWhatIsSafe(t *testing.T) {
	files := kit(t)
	files["index.html"] = append(files["index.html"], []byte(`
<a href="https://example.com/docs">docs</a>
<a href="#/screen/x">in page</a>
<img src="data:image/png;base64,iVBORw0KGgo=" alt="">
<img src="assets/logo.png" alt="">
<form onsubmit="return false"></form><form action="#"></form>
<script src="assets/more.js"></script>`)...)
	files["assets/more.js"] = []byte("var a = 'import and fetch are only words in a string: no call';\nvar importance = 1;\n")
	files["assets/logo.png"] = []byte("png")
	files["assets/f.woff2"] = []byte("font")
	files["assets/style.css"] = append(files["assets/style.css"], []byte("\n.x{background:url(data:image/png;base64,AAAA)}\n.y{background:url(logo.png)}\n")...)
	reseal(files)
	// the word "fetch" in a string does count: the checker is strict about the call it cannot tell from a mention
	v := prototype.Check(files)
	if got := rules(v); !strings.Contains(got, "no-network") || strings.Contains(got, "relative-urls") || strings.Contains(got, "forms") || strings.Contains(got, "classic-scripts") {
		t.Errorf("only the word 'fetch' should be flagged: %v", v)
	}
	files["assets/more.js"] = []byte("var importance = 1; var refetching = 2;\n")
	reseal(files)
	if v := prototype.Check(files); len(v) > 0 {
		t.Errorf("a safe prototype was refused: %v", v)
	}
}

func TestTheManifestHoldsEveryFileToItsHash(t *testing.T) {
	cases := map[string]func(map[string][]byte){
		"a file changed":  func(f map[string][]byte) { f["assets/style.css"] = append(f["assets/style.css"], '\n') },
		"a file added":    func(f map[string][]byte) { f["assets/extra.css"] = []byte("a{}") },
		"a file removed":  func(f map[string][]byte) { delete(f, "assets/data.js") },
		"no manifest":     func(f map[string][]byte) { delete(f, "manifest.json") },
		"a junk manifest": func(f map[string][]byte) { f["manifest.json"] = []byte("{") },
		"no revision":     func(f map[string][]byte) { f["manifest.json"] = []byte(`{"buildId":"x","files":{}}`) },
		"no entry page":   func(f map[string][]byte) { delete(f, "index.html"); reseal(f) },
	}
	for name, mutate := range cases {
		f := kit(t)
		mutate(f)
		v := prototype.Check(f)
		if len(v) == 0 {
			t.Errorf("%s was accepted", name)
		}
	}
	if v := prototype.Check(kit(t)); len(v) > 0 {
		t.Errorf("an untouched kit: %v", v)
	}
}

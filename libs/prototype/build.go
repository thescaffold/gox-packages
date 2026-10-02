package prototype

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"html"
	"sort"
	"strings"

	"github.com/thescaffold/gox-packages/libs/design"
)

//go:embed static/app.js static/style.css
var static embed.FS

// Generator names this builder in the manifest; change it when the kit changes, so a build says which kit made it.
const Generator = "origine-prototype-kit/1"

// Options say which revision the prototype shows.
type Options struct {
	Revision int
	BuildID  string
	// Rows is how many made-up rows each kind of data starts with; 4 when zero.
	Rows int
}

// Result is a finished prototype.
type Result struct {
	// Files maps a path inside the folder ("index.html", "assets/app.js") to its bytes; manifest.json is among them.
	Files map[string][]byte
	// Manifest is what manifest.json says.
	Manifest Manifest
}

// ErrNothingToShow: the design has no screens, so there is no prototype to make.
var ErrNothingToShow = errors.New("prototype: the design has no screens yet")

type protoField struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Options []string `json:"options,omitempty"`
	Ref     string   `json:"ref,omitempty"`
	Many    bool     `json:"many,omitempty"`
}

type protoCriterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type protoData struct {
	Title    string                     `json:"title"`
	Summary  string                     `json:"summary"`
	Revision int                        `json:"revision"`
	Actors   []map[string]string        `json:"actors"`
	Features []protoFeature             `json:"features"`
	Entities []protoEntity              `json:"entities"`
	Screens  []protoScreen              `json:"screens"`
	Limits   []map[string]string        `json:"limits"`
	Extra    map[string]json.RawMessage `json:"-"`
}

type protoFeature struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Summary  string           `json:"summary"`
	Priority string           `json:"priority"`
	Rules    []protoCriterion `json:"rules"`
	Criteria []protoCriterion `json:"criteria"`
}

type protoEntity struct {
	ID      string                   `json:"id"`
	Name    string                   `json:"name"`
	Summary string                   `json:"summary"`
	Fields  []protoField             `json:"fields"`
	Rows    []map[string]interface{} `json:"rows"`
}

type protoScreen struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Summary  string   `json:"summary"`
	Device   string   `json:"device"`
	Actions  []string `json:"actions"`
	Actors   []string `json:"actors"`
	Entities []string `json:"entities"`
	Features []string `json:"features"`
}

// Build makes the prototype of a design. The same design and options give the same bytes.
func Build(g *design.Graph, o Options) (*Result, error) {
	if g == nil || len(g.Of(design.Interface)) == 0 {
		return nil, ErrNothingToShow
	}
	if o.Revision < 1 {
		return nil, errors.New("prototype: a prototype shows one spec revision (1 or more)")
	}
	if o.BuildID == "" {
		o.BuildID = fmt.Sprintf("r%d", o.Revision)
	}
	rows := o.Rows
	if rows <= 0 {
		rows = 4
	}

	d := protoData{Title: g.Title, Summary: g.Summary, Revision: o.Revision, Actors: []map[string]string{}, Features: []protoFeature{}, Entities: []protoEntity{}, Screens: []protoScreen{}, Limits: []map[string]string{}}
	if d.Title == "" {
		d.Title = "Prototype"
	}
	for _, a := range g.Of(design.Actor) {
		d.Actors = append(d.Actors, map[string]string{"id": a.ID, "name": a.Name, "summary": a.Summary})
	}
	for _, f := range g.Of(design.Capability) {
		d.Features = append(d.Features, protoFeature{ID: f.ID, Name: f.Name, Summary: f.Summary, Priority: f.Priority, Rules: criteria(f.Rules), Criteria: criteria(f.Criteria)})
	}
	entityIDs := map[string]bool{}
	for _, e := range g.Of(design.Entity) {
		pe := protoEntity{ID: e.ID, Name: e.Name, Summary: e.Summary, Fields: []protoField{}}
		for _, f := range e.Fields {
			pe.Fields = append(pe.Fields, protoField{Name: f.Name, Type: f.Type, Options: f.Options, Ref: f.Ref, Many: f.Many})
		}
		entityIDs[e.ID] = true
		d.Entities = append(d.Entities, pe)
	}
	for i := range d.Entities {
		d.Entities[i].Rows = sampleRows(&d.Entities[i], d.Entities, g, rows)
	}
	for _, s := range g.Of(design.Interface) {
		ps := protoScreen{ID: s.ID, Name: s.Name, Summary: s.Summary, Device: s.Device, Actions: nonNil(s.Actions), Actors: []string{}, Entities: []string{}, Features: []string{}}
		seen := map[string]bool{}
		rel := func(id string) {
			if seen[id] {
				return
			}
			seen[id] = true
			n := g.Node(id)
			if n == nil {
				return
			}
			switch n.Kind {
			case design.Actor:
				ps.Actors = append(ps.Actors, id)
			case design.Entity:
				ps.Entities = append(ps.Entities, id)
			case design.Capability:
				ps.Features = append(ps.Features, id)
			}
		}
		for _, e := range g.From(s.ID) {
			rel(e.To)
		}
		for _, e := range g.To(s.ID) {
			rel(e.From)
		}
		sort.Strings(ps.Actors)
		sort.Strings(ps.Entities)
		sort.Strings(ps.Features)
		d.Screens = append(d.Screens, ps)
	}
	for _, l := range g.Limits {
		d.Limits = append(d.Limits, map[string]string{"kind": l.Kind, "text": l.Text})
	}

	dataJSON, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	appJS, err := static.ReadFile("static/app.js")
	if err != nil {
		return nil, err
	}
	css, err := static.ReadFile("static/style.css")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		"index.html":       []byte(indexHTML(d.Title)),
		"assets/style.css": css,
		"assets/data.js":   []byte("/* made-up data for the prototype; generated, not written by hand */\nwindow.PROTO = " + string(dataJSON) + ";\n"),
		"assets/app.js":    appJS,
	}
	m := Manifest{Format: 1, Revision: o.Revision, BuildID: o.BuildID, Title: d.Title, Generator: Generator, Files: map[string]string{}}
	for n, b := range files {
		m.Files[n] = sha(b)
	}
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	files["manifest.json"] = append(mb, '\n')

	if v := Check(files); len(v) > 0 {
		// the kit holds itself to its own rules: a violation here is a bug in the kit, never shipped
		return nil, fmt.Errorf("prototype: the kit made a prototype that breaks its own rules: %s", v[0])
	}
	return &Result{Files: files, Manifest: m}, nil
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func criteria(in []design.Criterion) []protoCriterion {
	out := []protoCriterion{}
	for _, c := range in {
		out = append(out, protoCriterion{ID: c.ID, Text: c.Text})
	}
	return out
}

func indexHTML(title string) string {
	t := html.EscapeString(title)
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + t + ` — prototype</title>
<link rel="stylesheet" href="assets/style.css">
</head>
<body>
<div id="app"><noscript><p style="padding:1rem">This prototype needs JavaScript.</p></noscript></div>
<script src="assets/data.js"></script>
<script src="assets/app.js"></script>
</body>
</html>
`
}

// ---- made-up rows ----

var (
	names    = []string{"Amara", "Chidi", "Ngozi", "Tunde", "Zainab", "Emeka", "Funmi", "Ibrahim", "Kemi", "Segun"}
	things   = []string{"Milk", "Rice", "Bread", "Eggs", "Tomatoes", "Beans", "Soap", "Tea", "Sugar", "Oil"}
	words    = []string{"Alpha", "Bravo", "Cedar", "Delta", "Ember", "Fjord", "Grove", "Harbor", "Indigo", "Juniper"}
	statuses = []string{"waiting", "paid", "delivered", "cancelled"}
)

func seed(parts ...string) uint32 {
	h := fnv.New32a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum32()
}

// sampleRows makes a few made-up rows for an entity, the same ones every time: values come from the names of the
// entity and its fields, never from a clock or a random source.
func sampleRows(e *protoEntity, all []protoEntity, g *design.Graph, n int) []map[string]interface{} {
	rows := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		row := map[string]interface{}{"id": fmt.Sprintf("%s-%d", e.ID, i+1)}
		for _, f := range e.Fields {
			row[f.Name] = sampleValue(e, f, all, g, i)
		}
		rows = append(rows, row)
	}
	return rows
}

func pick(list []string, s uint32, i int) string { return list[(int(s)+i)%len(list)] }

func sampleValue(e *protoEntity, f protoField, all []protoEntity, g *design.Graph, i int) interface{} {
	s := seed(e.ID, f.Name)
	lower := strings.ToLower(f.Name)
	switch strings.ToLower(f.Type) {
	case "number":
		return int(s%9) + 1 + i*2
	case "money":
		return float64(500+int(s%40)*250) + float64(i*1250)
	case "date":
		return fmt.Sprintf("2026-%02d-%02d", int(s%12)+1, (i*7+int(s%20))%28+1)
	case "time":
		return fmt.Sprintf("%02d:%02d", (int(s)+i*3)%12+8, (i*15)%60)
	case "yes/no":
		return (int(s)+i)%2 == 0
	case "email":
		return strings.ToLower(pick(names, s, i)) + "@example.com"
	case "phone":
		return fmt.Sprintf("+000 555 01%02d", (int(s)+i*7)%100)
	case "file":
		return fmt.Sprintf("document-%d.pdf", i+1)
	case "one of":
		if len(f.Options) > 0 {
			return f.Options[(int(s)+i)%len(f.Options)]
		}
		return pick(statuses, s, i)
	case "reference":
		for _, other := range all {
			if other.ID == f.Ref {
				// point at a row of the other entity (its ids are made the same way, so they exist)
				k := (int(s) + i) % 4
				if f.Many {
					return []string{fmt.Sprintf("%s-%d", other.ID, k+1), fmt.Sprintf("%s-%d", other.ID, (k+1)%4+1)}
				}
				return fmt.Sprintf("%s-%d", other.ID, k+1)
			}
		}
		// a reference to something that is not data (a kind of person): show who it is
		if n := g.Node(f.Ref); n != nil {
			return n.Name
		}
		return ""
	}
	// text, or anything else: a plausible word for what the field is called
	switch {
	case strings.Contains(lower, "name") || strings.Contains(lower, "customer") || strings.Contains(lower, "household") || strings.Contains(lower, "owner"):
		return pick(names, s, i)
	case strings.Contains(lower, "product") || strings.Contains(lower, "item") || strings.Contains(lower, "title"):
		return pick(things, s, i)
	case strings.Contains(lower, "status"):
		return pick(statuses, s, i)
	case strings.Contains(lower, "address") || strings.Contains(lower, "city"):
		return fmt.Sprintf("%d %s Street", (int(s)+i*13)%90+10, pick(words, s, i))
	}
	return pick(words, s, i) + " " + fmt.Sprint(i+1)
}

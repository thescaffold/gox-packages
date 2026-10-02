package design

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// NodeChange is a node that was added, removed or changed between two graphs.
type NodeChange struct {
	Op   string // "added", "removed" or "changed"
	ID   string
	Kind NodeKind
	Name string
	// Fields names what changed on a node that was changed (its JSON field names).
	Fields []string
}

// EdgeChange is an edge that was added or removed.
type EdgeChange struct {
	Op   string // "added" or "removed"
	Edge Edge
}

// Delta is what differs between two graphs, by id.
type Delta struct {
	Nodes []NodeChange
	Edges []EdgeChange
}

// Empty reports whether nothing differs.
func (d Delta) Empty() bool { return len(d.Nodes) == 0 && len(d.Edges) == 0 }

// Compare lists the differences between two graphs. Where a node's code
// ownership is, whether it is marked inferred and how its description is worded
// are not part of the design's behaviour and are not compared; a rename (the name
// field) is.
func Compare(before, after *Graph) Delta {
	var d Delta
	bn := map[string]Node{}
	for _, n := range before.Nodes {
		bn[n.ID] = n
	}
	an := map[string]Node{}
	for _, n := range after.Nodes {
		an[n.ID] = n
	}
	for _, n := range after.Nodes {
		old, had := bn[n.ID]
		switch {
		case !had:
			d.Nodes = append(d.Nodes, NodeChange{Op: "added", ID: n.ID, Kind: n.Kind, Name: n.Name})
		default:
			if f := changedFields(old, n); len(f) > 0 {
				d.Nodes = append(d.Nodes, NodeChange{Op: "changed", ID: n.ID, Kind: n.Kind, Name: n.Name, Fields: f})
			}
		}
	}
	for _, n := range before.Nodes {
		if _, still := an[n.ID]; !still {
			d.Nodes = append(d.Nodes, NodeChange{Op: "removed", ID: n.ID, Kind: n.Kind, Name: n.Name})
		}
	}
	key := func(e Edge) string { return e.From + "\x00" + string(e.Kind) + "\x00" + e.To }
	be := map[string]bool{}
	for _, e := range before.Edges {
		be[key(e)] = true
	}
	ae := map[string]bool{}
	for _, e := range after.Edges {
		ae[key(e)] = true
	}
	for _, e := range after.Edges {
		if !be[key(e)] {
			d.Edges = append(d.Edges, EdgeChange{Op: "added", Edge: e})
		}
	}
	for _, e := range before.Edges {
		if !ae[key(e)] {
			d.Edges = append(d.Edges, EdgeChange{Op: "removed", Edge: e})
		}
	}
	return d
}

// changedFields names the fields that differ between two versions of a node.
func changedFields(a, b Node) []string {
	var out []string
	ta := reflect.TypeOf(a)
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	for i := 0; i < ta.NumField(); i++ {
		name, _, _ := strings.Cut(ta.Field(i).Tag.Get("json"), ",")
		if name == "ownership" || name == "inferred" || name == "id" || name == "summary" {
			continue
		}
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			out = append(out, name)
		}
	}
	return out
}

// Affected is a thing in the design that is now out of date.
type Affected struct {
	ID   string   `json:"id"`
	Kind NodeKind `json:"kind"`
	Name string   `json:"name"`
	// Direct is true when the change was to this thing itself (or to a link it has),
	// false when it is affected because something it depends on changed.
	Direct  bool `json:"direct,omitempty"`
	Removed bool `json:"removed,omitempty"`
	// Why says so in plain words.
	Why string `json:"why"`
}

// TestRef is an acceptance criterion that has to be (re)checked.
type TestRef struct {
	Capability string `json:"capability"`
	Criterion  string `json:"criterion"`
	Text       string `json:"text"`
	// Status is "rerun" (unchanged, but its capability is affected), "new", "changed" or "removed".
	Status string `json:"status"`
}

// CodeRef is the code that belongs to an affected part of the design.
type CodeRef struct {
	Node  string   `json:"node"`
	Paths []string `json:"paths"`
}

// Task is a piece of work the change implies, in plain words. It is the plan a
// build starts from.
type Task struct {
	// Kind is "add", "remove", "update", "move", "connect", "disconnect", "deploy" or "recheck".
	Kind  string `json:"kind"`
	Node  string `json:"node"`
	Title string `json:"title"`
	Why   string `json:"why"`
}

// ImpactSet is everything a change to the design makes stale.
type ImpactSet struct {
	Delta Delta
	// Affected lists the parts of the design that are out of date, in graph
	// order, with removed ones last.
	Affected []Affected
	// Tests are the criteria of affected capabilities: all of them to be re-run,
	// with the ones that are new, changed or removed marked as such.
	Tests []TestRef
	// Code is the code owned by affected parts (before or after, so code of
	// something removed or moved is included); Unbuilt names affected parts that
	// have no code yet.
	Code    []CodeRef
	Unbuilt []string
	// Artifacts are the generated files that must be made again.
	Artifacts []string
	// Work is the plan: what to build, change, move, connect and re-check.
	Work []Task
}

// Empty reports whether the change affects nothing.
func (s ImpactSet) Empty() bool { return len(s.Affected) == 0 && s.Delta.Empty() }

// IDs lists the ids of affected parts of one kind.
func (s ImpactSet) IDs(kind NodeKind) []string {
	var out []string
	for _, a := range s.Affected {
		if a.Kind == kind {
			out = append(out, a.ID)
		}
	}
	return out
}

// dependents says, for each node, which nodes are out of date when it changes.
// Something that uses or calls a thing depends on it; an owner depends on what it
// owns (a service changes when the data it keeps does); an environment depends on
// whatever is deployed to it. Actors and environments are ends of the line: a
// change to what a person uses does not make the person stale, and a redeployed
// environment does not make what runs in it stale.
func dependents(g ...*Graph) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	add := func(of, who string) {
		k := of + "\x00" + who
		if of == who || seen[k] {
			return
		}
		seen[k] = true
		out[of] = append(out[of], who)
	}
	for _, gr := range g {
		for _, e := range gr.Edges {
			switch e.Kind {
			case Uses, Calls:
				add(e.To, e.From)
			case Owns:
				add(e.To, e.From)
			case DeploysTo:
				add(e.From, e.To)
			}
		}
	}
	return out
}

// Impact works out what a change to the design makes stale: the parts of the
// design that changed or depend on something that did, the tests to re-run, the
// code those parts own, the generated files to make again, and the work it adds up to.
func Impact(before, after *Graph) ImpactSet {
	delta := Compare(before, after)
	s := ImpactSet{Delta: delta}
	kind := map[string]NodeKind{}
	name := map[string]string{}
	node := map[string]Node{}
	for _, gr := range []*Graph{before, after} {
		for _, n := range gr.Nodes {
			kind[n.ID], name[n.ID], node[n.ID] = n.Kind, n.Name, n
		}
	}
	in := func(g *Graph, id string) bool { return g.Node(id) != nil }

	why := map[string]string{}
	direct := map[string]bool{}
	removed := map[string]bool{}
	var queue []string
	touch := func(id, reason string) {
		if _, ok := why[id]; ok {
			return
		}
		why[id], direct[id] = reason, true
		queue = append(queue, id)
	}
	for _, c := range delta.Nodes {
		switch c.Op {
		case "added":
			touch(c.ID, "it is new")
		case "removed":
			removed[c.ID] = true
			touch(c.ID, "it was removed")
		case "changed":
			touch(c.ID, "its "+strings.Join(c.Fields, ", ")+" changed")
		}
	}
	for _, c := range delta.Edges {
		e := c.Edge
		describe := func(self, other string) string {
			switch c.Op {
			case "added":
				return fmt.Sprintf("it is now linked to “%s” (%s)", name[other], e.Kind)
			}
			return fmt.Sprintf("its link to “%s” (%s) was removed", name[other], e.Kind)
		}
		for _, end := range [][2]string{{e.From, e.To}, {e.To, e.From}} {
			if kind[end[0]] == Actor {
				continue
			}
			touch(end[0], describe(end[0], end[1]))
		}
	}
	// propagate to what depends on them
	deps := dependents(before, after)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, d := range deps[id] { // an environment has no dependents, so the change ends there
			if kind[d] == Actor {
				continue
			}
			if _, ok := why[d]; ok {
				continue
			}
			why[d] = fmt.Sprintf("%s “%s”, which is affected", dependsPhrase(kind[d], kind[id]), name[id])
			queue = append(queue, d)
		}
	}

	// order: graph order for what is still there, then what was removed
	for _, n := range after.Nodes {
		if _, ok := why[n.ID]; ok {
			s.Affected = append(s.Affected, Affected{ID: n.ID, Kind: n.Kind, Name: n.Name, Direct: direct[n.ID], Why: why[n.ID]})
		}
	}
	for _, n := range before.Nodes {
		if removed[n.ID] {
			s.Affected = append(s.Affected, Affected{ID: n.ID, Kind: n.Kind, Name: n.Name, Direct: true, Removed: true, Why: why[n.ID]})
		}
	}

	s.Tests = testsFor(before, after, why, removed)
	s.Code, s.Unbuilt = codeFor(before, after, s.Affected)
	s.Artifacts = artifactsFor(delta, s.Affected, kind)
	s.Work = workFor(before, after, delta, s.Affected, name, in)
	return s
}

func dependsPhrase(dependent, on NodeKind) string {
	switch {
	case dependent == Environment:
		return "something deployed to it changed:"
	case on == Entity:
		return "it works with"
	case on == Integration:
		return "it uses"
	}
	return "it depends on"
}

// testsFor lists the criteria of the capabilities that are affected.
func testsFor(before, after *Graph, why map[string]string, removed map[string]bool) []TestRef {
	var out []TestRef
	old := map[string]Criterion{}
	for _, n := range before.Nodes {
		for _, c := range n.Criteria {
			old[c.ID] = c
		}
	}
	now := map[string]bool{}
	for _, n := range after.Nodes {
		for _, c := range n.Criteria {
			now[c.ID] = true
		}
	}
	for _, n := range after.Nodes {
		if n.Kind != Capability {
			continue
		}
		if _, ok := why[n.ID]; !ok {
			continue // a capability with a new or reworded criterion is itself changed, so it is never here
		}
		for _, c := range n.Criteria {
			status := "rerun"
			if o, had := old[c.ID]; !had {
				status = "new"
			} else if o.Text != c.Text {
				status = "changed"
			}
			out = append(out, TestRef{Capability: n.ID, Criterion: c.ID, Text: c.Text, Status: status})
		}
	}
	for _, n := range before.Nodes {
		if n.Kind != Capability {
			continue
		}
		for _, c := range n.Criteria {
			if !now[c.ID] {
				out = append(out, TestRef{Capability: n.ID, Criterion: c.ID, Text: c.Text, Status: "removed"})
			}
		}
	}
	return out
}

func codeFor(before, after *Graph, affected []Affected) ([]CodeRef, []string) {
	var code []CodeRef
	var unbuilt []string
	for _, a := range affected {
		var paths []string
		for _, gr := range []*Graph{after, before} {
			if n := gr.Node(a.ID); n != nil {
				paths = append(paths, n.Ownership...)
			}
		}
		paths = uniqueStrings(paths)
		if len(paths) == 0 {
			if !a.Removed && a.Kind != Actor && a.Kind != Environment {
				unbuilt = append(unbuilt, a.ID)
			}
			continue
		}
		code = append(code, CodeRef{Node: a.ID, Paths: paths})
	}
	return code, unbuilt
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// artifactsFor lists the generated files that have to be made again, in a fixed order.
func artifactsFor(d Delta, affected []Affected, kind map[string]NodeKind) []string {
	if d.Empty() {
		return nil
	}
	out := []string{"design.json", "design.mmd"}
	// the entity diagram changes when data is added, removed or reshaped, or entities link differently
	erd := false
	for _, c := range d.Nodes {
		if c.Kind == Entity {
			erd = true
		}
	}
	for _, c := range d.Edges {
		if kind[c.Edge.From] == Entity && (kind[c.Edge.To] == Entity || kind[c.Edge.To] == Actor) {
			erd = true
		}
	}
	if erd {
		out = append(out, "erd.mmd")
	}
	api, proto := false, false
	for _, a := range affected {
		switch a.Kind {
		case Service, Job, Interface:
			api = api || a.Direct
		case Capability:
			api = api || a.Direct
		}
		if a.Kind == Interface || a.Kind == Capability {
			proto = true
		}
	}
	if api {
		out = append(out, "openapi.yaml")
	}
	if proto {
		out = append(out, "prototype")
	}
	return append(out, "PRD.md", "TRD.md")
}

func list(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func quote(s string) string { return "“" + s + "”" }

func addTitle(k NodeKind, n string) string {
	switch k {
	case Actor:
		return "Add the kind of person " + quote(n)
	case Capability:
		return "Build " + quote(n)
	case Service:
		return "Create the " + quote(n) + " service"
	case Entity:
		return "Add the " + quote(n) + " data"
	case Datastore:
		return "Set up the " + quote(n) + " storage"
	case Interface:
		return "Build the " + quote(n) + " screen"
	case Integration:
		return "Connect " + quote(n)
	case Job:
		return "Create the " + quote(n) + " job"
	case Environment:
		return "Set up the " + quote(n) + " environment"
	}
	return "Add " + quote(n)
}

// workFor turns a delta into the plan: one task for each thing that has to be
// built, removed, updated, moved, connected, deployed or looked at again.
func workFor(before, after *Graph, d Delta, affected []Affected, name map[string]string, in func(*Graph, string) bool) []Task {
	var out []Task
	added := map[string]bool{}
	removed := map[string]bool{}
	for _, c := range d.Nodes {
		switch c.Op {
		case "added":
			added[c.ID] = true
		case "removed":
			removed[c.ID] = true
		}
	}
	for _, c := range d.Nodes {
		switch c.Op {
		case "added":
			out = append(out, Task{Kind: "add", Node: c.ID, Title: addTitle(c.Kind, c.Name), Why: "the spec now has it"})
		case "removed":
			out = append(out, Task{Kind: "remove", Node: c.ID, Title: "Remove " + quote(c.Name), Why: "the spec no longer has it"})
		case "changed":
			out = append(out, Task{Kind: "update", Node: c.ID, Title: "Update " + quote(c.Name) + ": " + strings.Join(c.Fields, ", "), Why: "the spec changed it"})
		}
	}

	// who owned and who owns each thing
	oldOwner, newOwner := map[string]string{}, map[string]string{}
	for _, e := range before.Edges {
		if e.Kind == Owns {
			oldOwner[e.To] = e.From
		}
	}
	for _, e := range after.Edges {
		if e.Kind == Owns {
			newOwner[e.To] = e.From
		}
	}
	var moved []string
	for _, n := range after.Nodes {
		if o, ok := newOwner[n.ID]; ok && oldOwner[n.ID] != o && !added[n.ID] {
			moved = append(moved, n.ID)
		}
	}
	for _, id := range moved {
		title := "Move " + quote(name[id]) + " into " + quote(name[newOwner[id]])
		if from, had := oldOwner[id]; had && !removed[from] {
			title = "Move " + quote(name[id]) + " from " + quote(name[from]) + " to " + quote(name[newOwner[id]])
		}
		out = append(out, Task{Kind: "move", Node: id, Title: title, Why: "who owns it changed"})
	}

	// something that lost its owner and was not given another
	for _, n := range before.Nodes {
		if from, had := oldOwner[n.ID]; had && newOwner[n.ID] == "" && !removed[n.ID] {
			if removed[from] {
				out = append(out, Task{Kind: "move", Node: n.ID, Title: "Find a new owner for " + quote(name[n.ID]) + " (it belonged to " + quote(name[from]) + ")", Why: "what owned it was removed"})
			} else {
				out = append(out, Task{Kind: "move", Node: n.ID, Title: "Take " + quote(name[n.ID]) + " out of " + quote(name[from]), Why: "it no longer belongs to it"})
			}
		}
	}

	deployed := map[string][]string{}
	var deployOrder []string
	// links that went away first, then links that appeared
	for _, op := range []string{"removed", "added"} {
		for _, c := range d.Edges {
			e := c.Edge
			if c.Op != op {
				continue
			}
			switch {
			case e.Kind == Owns:
			case e.Kind == DeploysTo:
				if op == "added" && in(after, e.To) {
					if _, ok := deployed[e.From]; !ok {
						deployOrder = append(deployOrder, e.From)
					}
					deployed[e.From] = append(deployed[e.From], name[e.To])
				}
			case op == "removed":
				if !removed[e.From] && !removed[e.To] {
					out = append(out, Task{Kind: "disconnect", Node: e.From, Title: "Disconnect " + quote(name[e.From]) + " from " + quote(name[e.To]), Why: "the spec no longer links them (" + string(e.Kind) + ")"})
				}
			default:
				if kindOf(after, e.From) == Actor {
					continue
				}
				out = append(out, Task{Kind: "connect", Node: e.From, Title: "Connect " + quote(name[e.From]) + " to " + quote(name[e.To]), Why: "the spec now links them (" + string(e.Kind) + ")"})
			}
		}
	}
	for _, id := range deployOrder {
		out = append(out, Task{Kind: "deploy", Node: id, Title: "Deploy " + quote(name[id]) + " to " + list(deployed[id]), Why: "the spec now says where it runs"})
	}
	for _, a := range affected {
		if a.Direct || a.Removed || a.Kind == Actor {
			continue
		}
		if a.Kind == Environment {
			out = append(out, Task{Kind: "deploy", Node: a.ID, Title: "Redeploy " + quote(a.Name), Why: a.Why})
			continue
		}
		out = append(out, Task{Kind: "recheck", Node: a.ID, Title: "Re-check " + quote(a.Name), Why: a.Why})
	}
	return out
}

func kindOf(g *Graph, id string) NodeKind {
	if n := g.Node(id); n != nil {
		return n.Kind
	}
	return ""
}

// JSON is the impact set as stable JSON, for storing with a proposal.
func (s ImpactSet) JSON() ([]byte, error) {
	type wire struct {
		Affected  []Affected `json:"affected"`
		Tests     []TestRef  `json:"tests"`
		Code      []CodeRef  `json:"code"`
		Unbuilt   []string   `json:"unbuilt"`
		Artifacts []string   `json:"artifacts"`
		Work      []Task     `json:"work"`
	}
	w := wire{s.Affected, s.Tests, s.Code, s.Unbuilt, s.Artifacts, s.Work}
	if w.Affected == nil {
		w.Affected = []Affected{}
	}
	if w.Tests == nil {
		w.Tests = []TestRef{}
	}
	if w.Code == nil {
		w.Code = []CodeRef{}
	}
	if w.Unbuilt == nil {
		w.Unbuilt = []string{}
	}
	if w.Artifacts == nil {
		w.Artifacts = []string{}
	}
	if w.Work == nil {
		w.Work = []Task{}
	}
	return json.MarshalIndent(w, "", "  ")
}

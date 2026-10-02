package spec

import "github.com/thescaffold/gox-packages/libs/design"

// Impact says what a change to a spec makes stale: it compiles both revisions,
// gives each node the code that owns it (ownership maps a node id to path
// prefixes; nil when nothing is built yet) and compares the two designs. The
// result lists the capabilities, screens and environments that are out of date,
// the tests to run again, the code involved, the files to generate again and the
// work that adds up to (TRD §6.5, §6.18).
func Impact(before, after *Doc, ownership map[string][]string) (design.ImpactSet, []Diagnostic) {
	gb, db := Compile(before)
	ga, da := Compile(after)
	for _, g := range []*design.Graph{gb, ga} {
		for i := range g.Nodes {
			if paths, ok := ownership[g.Nodes[i].ID]; ok {
				g.Nodes[i].Ownership = append([]string(nil), paths...)
			}
		}
	}
	return design.Impact(gb, ga), append(db, da...)
}

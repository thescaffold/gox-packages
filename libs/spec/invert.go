package spec

import (
	"fmt"
	"strings"
)

// Invert gives the patch that undoes p: applying p to d and then the result
// gives back d exactly. d must be canonical (every item has an id). It works from
// what p actually changes, so it undoes splits and merges and the reference
// rewrites a rename makes as well as plain edits.
func Invert(d *Doc, p SpecPatch) (SpecPatch, error) {
	norm, err := Apply(d, SpecPatch{})
	if err != nil {
		return SpecPatch{}, err
	}
	if Print(norm) != Print(d) {
		return SpecPatch{}, fmt.Errorf("some items have no id yet, so a patch cannot be undone exactly; canonicalize the document first")
	}
	after, err := Apply(d, p)
	if err != nil {
		return SpecPatch{}, err
	}
	steps, err := stepsBetween(after, d)
	if err != nil {
		return SpecPatch{}, err
	}
	inv := SpecPatch{Base: p.Base + 1, Ops: steps}
	if p.Summary != "" {
		inv.Summary = "Undo: " + p.Summary
	}
	// whatever the steps are, they must really do it
	back, err := Apply(after, inv)
	if err != nil || Print(back) != Print(d) {
		return SpecPatch{}, fmt.Errorf("this patch cannot be undone exactly")
	}
	return inv, nil
}

type place struct {
	sec int
	idx int
	it  *Item
}

func itemPlaces(d *Doc) map[string]place {
	m := map[string]place{}
	for si, s := range d.Sections {
		for ni, n := range s.Nodes {
			if it, ok := n.(*Item); ok && it.ID != "" {
				m[it.ID] = place{sec: si, idx: ni, it: it}
			}
		}
	}
	return m
}

func sectionIDs(s *Section) []string {
	var ids []string
	for _, n := range s.Nodes {
		if it, ok := n.(*Item); ok && it.ID != "" {
			ids = append(ids, it.ID)
		}
	}
	return ids
}

// stepsBetween gives the operations that turn a into b, when b has the same
// sections as a plus possibly fewer (a patch can add a section, never remove one).
func stepsBetween(a, b *Doc) ([]PatchOp, error) {
	for i := 0; i < len(a.Sections) && i < len(b.Sections); i++ {
		if a.Sections[i].Name != b.Sections[i].Name {
			return nil, fmt.Errorf("the sections changed order")
		}
	}
	// sections that b has and a lost (the undo of a prune) come back empty, first
	var makes []PatchOp
	for si := len(a.Sections); si < len(b.Sections); si++ {
		makes = append(makes, PatchOp{Op: "section", Section: b.Sections[si].Name})
	}
	ap, bp := itemPlaces(a), itemPlaces(b)
	var removes, replaces, restores, prunes []PatchOp
	for si, bs := range b.Sections {
		var aIDs []string
		if si < len(a.Sections) {
			aIDs = sectionIDs(a.Sections[si])
		}
		bIDs := sectionIDs(bs)
		bSet := map[string]bool{}
		for _, id := range bIDs {
			bSet[id] = true
		}
		var commonA, commonB []string
		for _, id := range aIDs {
			if bSet[id] && bp[id].sec == si {
				commonA = append(commonA, id)
			}
		}
		for _, id := range bIDs {
			if q, ok := ap[id]; ok && q.sec == si {
				commonB = append(commonB, id)
			}
		}
		reinsert := strings.Join(commonA, "\x00") != strings.Join(commonB, "\x00")
		stable := map[string]bool{}
		for _, id := range commonB {
			stable[id] = !reinsert
		}
		for _, id := range aIDs {
			if _, ok := stable[id]; !ok || !stable[id] {
				removes = append(removes, PatchOp{Op: "remove", Item: id})
			}
		}
		for _, id := range bIDs {
			q := bp[id]
			if stable[id] {
				if itemText(ap[id].it) != itemText(q.it) {
					snap := snapOf(q.it)
					replaces = append(replaces, PatchOp{Op: "replace", Item: id, Snap: &snap})
				}
				continue
			}
			snap, idx := snapOf(q.it), q.idx
			restores = append(restores, PatchOp{Op: "restore", Section: bs.Name, Index: &idx, Snap: &snap})
		}
	}
	// an item that a patch moved into a section from a later one is removed there
	for si := len(b.Sections); si < len(a.Sections); si++ {
		for _, id := range sectionIDs(a.Sections[si]) {
			removes = append(removes, PatchOp{Op: "remove", Item: id})
		}
	}
	for si := len(a.Sections) - 1; si >= len(b.Sections); si-- {
		prunes = append(prunes, PatchOp{Op: "prune", Section: a.Sections[si].Name})
	}
	var steps []PatchOp
	steps = append(steps, removes...)
	steps = append(steps, makes...)
	steps = append(steps, replaces...)
	steps = append(steps, restores...)
	steps = append(steps, prunes...)
	return steps, nil
}

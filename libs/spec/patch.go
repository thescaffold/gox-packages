package spec

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Strings is a JSON value that is either one string or a list of them.
type Strings []string

func (s *Strings) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*s = Strings{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("expected a string or a list of strings")
	}
	*s = many
	return nil
}

// SpecPatch is how the AI and the chat edit a spec (Appendix E.5): an ordered
// list of operations, each addressed by id, applied all together or not at all.
type SpecPatch struct {
	// Base is the revision number the patch was written against.
	Base    int       `json:"base"`
	Summary string    `json:"summary,omitempty"`
	Ops     []PatchOp `json:"ops"`
}

// PatchOp is one step of a patch. Op is one of add, set, text, move, remove,
// rename, split, merge, answer; Invert also produces replace, restore, prune and section.
type PatchOp struct {
	Op string `json:"op"`
	// Item is the id the step acts on; Items are the ids a merge joins.
	Item  string   `json:"item,omitempty"`
	Items []string `json:"items,omitempty"`
	// Parent is where an item is added or moved to: a section name ("parts") or,
	// for a rule, criterion or field, the id of the item it belongs to.
	Parent string `json:"parent,omitempty"`
	// After places the step's result after that sibling's id; "" means first and
	// leaving it out means last.
	After *string `json:"after,omitempty"`
	// List names the list an entry is added to: "must", "done when", "not now" or "fields".
	List    string             `json:"list,omitempty"`
	Kind    string             `json:"kind,omitempty"`
	ID      string             `json:"id,omitempty"`
	Title   string             `json:"title,omitempty"`
	Text    string             `json:"text,omitempty"`
	Style   string             `json:"style,omitempty"` // "block" or "bullet", to override a kind's usual form
	Props   map[string]Strings `json:"props,omitempty"`
	Options []string           `json:"options,omitempty"`
	// Prop, Value, Add and Remove edit one property: Value replaces it, Add and
	// Remove change the comma-separated list in it.
	Prop   string   `json:"prop,omitempty"`
	Value  *Strings `json:"value,omitempty"`
	Add    Strings  `json:"add,omitempty"`
	Remove Strings  `json:"remove,omitempty"`
	Answer *string  `json:"answer,omitempty"`
	// Into are the new items a split makes (each an "add" without a parent).
	Into   []PatchOp `json:"into,omitempty"`
	Reason string    `json:"reason,omitempty"`

	// produced by Invert
	Snap    *ItemSnap `json:"snap,omitempty"`
	Index   *int      `json:"index,omitempty"`
	Section string    `json:"section,omitempty"`
}

// PatchError says which step of a patch failed and why, in plain words.
type PatchError struct {
	Step int // 0-based
	Op   string
	Msg  string
}

func (e *PatchError) Error() string {
	return fmt.Sprintf("step %d (%s): %s", e.Step+1, e.Op, e.Msg)
}

// ParsePatch reads a patch from JSON.
func ParsePatch(b []byte) (SpecPatch, error) {
	var p SpecPatch
	if err := json.Unmarshal(b, &p); err != nil {
		return SpecPatch{}, fmt.Errorf("this is not a valid patch: %v", err)
	}
	return p, nil
}

// Check reports what is wrong with a patch before it is tried: an unknown
// operation, a missing target, or (when needReasons is set) a step without a
// one-line reason. The AI's patches need reasons; the ones Invert makes do not.
func (p SpecPatch) Check(needReasons bool) error {
	if len(p.Ops) == 0 {
		return fmt.Errorf("the patch has no steps")
	}
	for i, op := range p.Ops {
		if msg := op.shapeProblem(); msg != "" {
			return &PatchError{Step: i, Op: op.Op, Msg: msg}
		}
		if needReasons && strings.TrimSpace(op.Reason) == "" {
			return &PatchError{Step: i, Op: op.Op, Msg: "every step needs a one-line reason"}
		}
	}
	return nil
}

// shapeProblem says what a step lacks to be tried at all.
func (op PatchOp) shapeProblem() string {
	switch op.Op {
	case "add", "set", "text", "move", "remove", "rename", "split", "merge", "answer", "replace", "restore", "prune", "section":
	default:
		return fmt.Sprintf("“%s” is not something a patch can do", op.Op)
	}
	switch op.Op {
	case "set", "text", "move", "remove", "rename", "split", "answer", "replace":
		if op.Item == "" {
			return "it does not say which item it is about"
		}
	case "merge":
		if len(op.Items) < 2 {
			return "a merge needs at least two items"
		}
	case "add":
		if op.Parent == "" {
			return "it does not say where to add it"
		}
	}
	return ""
}

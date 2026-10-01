package spec

import (
	"fmt"
	"sort"
	"strings"
)

// Validate checks a document. Errors are structural and block a commit
// (an id used twice, an id that cannot be one); warnings do not (a reference
// that points nowhere, a question without choices, a feature with nothing that
// shows it works, something that looks like a secret).
func Validate(d *Doc) []Diagnostic {
	var out []Diagnostic
	diag := func(line int, sev Severity, code, msg string) {
		out = append(out, Diagnostic{Line: line, Col: 1, Severity: sev, Code: code, Message: msg})
	}

	// ids
	seen := map[string]bool{}
	for _, a := range d.Addressables() {
		switch {
		case !validID(a.ID):
			diag(a.Line, Error, "id-invalid", fmt.Sprintf("“%s” cannot be an id. Ids are letters, numbers, - and _.", a.ID))
		case seen[a.ID]:
			diag(a.Line, Error, "dup-id", fmt.Sprintf("The id “%s” is used more than once. Every item needs its own id.", a.ID))
		}
		seen[a.ID] = true
	}

	// references
	d.eachTextAt(func(s *string, line int) {
		for _, r := range References(*s) {
			switch ids := d.refMatches(r); len(ids) {
			case 1:
			case 0:
				what := "“" + r.Inner + "”"
				if r.ByID {
					what = "the id “" + r.Inner + "”"
				}
				diag(line, Warning, "unresolved-ref", fmt.Sprintf("%s does not match anything in the spec.", what))
			default:
				diag(line, Warning, "ambiguous-ref", fmt.Sprintf("“%s” could mean more than one item (%s). Use one of their ids.", r.Inner, strings.Join(ids, ", ")))
			}
		}
	})

	// items
	for _, s := range d.Sections {
		for _, n := range s.Nodes {
			it, ok := n.(*Item)
			if !ok {
				continue
			}
			if it.Style == StyleQuestion && len(it.Options) == 0 && !it.HasAnswer {
				diag(it.Line, Warning, "question-no-choices", "This question has no choices and no answer. Add choices so it can be answered with one tap.")
			}
			if it.Kind == "feature" && it.Style == StyleBlock && !hasEntries(it, "done when") {
				diag(it.Line, Warning, "feature-no-checks", fmt.Sprintf("“%s” has no “done when” checks, so nothing can show that it works.", itemLabel(it)))
			}
		}
	}

	// secrets: scanned on the canonical text, so the lines are those of Print
	out = append(out, ScanSecrets(Print(d))...)

	sort.SliceStable(out, func(i, j int) bool { return out[i].Severity > out[j].Severity })
	return out
}

func hasEntries(it *Item, key string) bool {
	for _, b := range it.Body {
		if lp, ok := b.(*ListProp); ok && lp.Key == key && len(lp.Entries) > 0 {
			return true
		}
	}
	return false
}

// refMatches lists the ids a reference could mean: an id names itself if it
// exists; a title names every titled item that carries it.
func (d *Doc) refMatches(r Ref) []string {
	if r.ByID {
		for _, a := range d.Addressables() {
			if a.ID == r.Inner {
				return []string{a.ID}
			}
		}
		return nil
	}
	want := normTitle(r.Inner)
	var ids []string
	for _, s := range d.Sections {
		for _, n := range s.Nodes {
			if it, ok := n.(*Item); ok && it.Title != "" && it.ID != "" && normTitle(it.Title) == want {
				ids = append(ids, it.ID)
			}
		}
	}
	return ids
}

// HasErrors reports whether any diagnostic blocks a commit.
func HasErrors(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

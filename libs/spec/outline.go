package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// OutlineItem is one line of the compact view a model is given: enough to know
// what exists and to ask for the rest by id.
type OutlineItem struct {
	ID    string
	Kind  string
	Title string
	// Status is derived, never typed: a question is "open" until it has an answer,
	// an assumption is "assumed" until it is moved to Decisions, everything else is "decided".
	Status string
	// Parent is the id of the item a rule, criterion or field belongs to.
	Parent string
	// Path says where it sits: its section ("features"), or the item it belongs
	// to under that ("features/recurring-orders").
	Path string
	// Hash is a sha-256 of what it says, so a change to it is a different hash and
	// nothing else is.
	Hash string
}

func hashOf(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}

// sectionPath is a section's name as a path segment: "Ideas for later" is "ideas-for-later".
func sectionPath(name string) string {
	return slug(name, 8, "notes")
}

// Outline lists every addressable thing in document order.
func Outline(d *Doc) []OutlineItem {
	var out []OutlineItem
	for _, s := range d.Sections {
		path := sectionPath(s.Name)
		for _, n := range s.Nodes {
			it, ok := n.(*Item)
			if !ok {
				continue
			}
			if it.ID != "" {
				out = append(out, OutlineItem{ID: it.ID, Kind: it.Kind, Title: clip(itemLabel(it), 80), Status: statusOf(it), Path: path, Hash: hashOf(itemText(it))})
			}
			for _, b := range it.Body {
				switch b := b.(type) {
				case *ListProp:
					kind, ok := identifiedLists[b.Key]
					if !ok {
						kind = "child"
					}
					for _, e := range b.Entries {
						if e.ID != "" {
							out = append(out, OutlineItem{ID: e.ID, Kind: kind, Title: clip(e.Text, 80), Status: "decided", Parent: it.ID, Path: path + "/" + it.ID, Hash: hashOf(kind, e.ID, e.Text)})
						}
					}
				case *Child:
					if b.ID != "" {
						out = append(out, OutlineItem{ID: b.ID, Kind: "child", Title: clip(b.Text, 80), Status: "decided", Parent: it.ID, Path: path + "/" + it.ID, Hash: hashOf("child", b.ID, b.Text)})
					}
				}
			}
		}
	}
	return out
}

func statusOf(it *Item) string {
	switch {
	case it.Style == StyleQuestion && it.HasAnswer:
		return "answered"
	case it.Style == StyleQuestion:
		return "open"
	case it.Kind == "assumption":
		return "assumed"
	}
	return "decided"
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// OutlineText is the outline as text: one item per line, rules and criteria
// indented under the item they belong to.
func OutlineText(d *Doc) string {
	var sb strings.Builder
	title := d.Title
	if title == "" {
		title = "(untitled)"
	}
	fmt.Fprintf(&sb, "%s\n", title)
	for _, o := range Outline(d) {
		indent := ""
		if o.Parent != "" {
			indent = "  "
		}
		fmt.Fprintf(&sb, "%s%s · %s · %s · %s\n", indent, o.ID, o.Kind, o.Title, o.Status)
	}
	return sb.String()
}

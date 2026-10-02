package spec

import (
	"encoding/json"
)

// astDoc is the parsed form of a document, as plain data: what a program reads
// instead of the text (TRD §5.1 keeps it beside every revision as "spec.json").
type astDoc struct {
	Version  string       `json:"ospec"`
	Title    string       `json:"title"`
	Summary  string       `json:"summary,omitempty"`
	Settings []astSetting `json:"settings,omitempty"`
	Sections []astSection `json:"sections"`
}

type astSetting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type astSection struct {
	Name  string     `json:"name"`
	Kind  string     `json:"kind"`
	Items []ItemSnap `json:"items"`
	// Notes is the free text in the section (a note section's paragraphs).
	Notes []string `json:"notes,omitempty"`
}

// JSON is the parsed form of the document: its settings and, for each section,
// every item with its properties, lists, fields and questions. Free text outside
// items is kept as notes. It is stable: the same document gives the same bytes.
func (d *Doc) JSON() ([]byte, error) {
	a := astDoc{Version: d.Version, Title: d.Title, Summary: d.Summary, Sections: []astSection{}}
	for _, s := range d.Settings {
		a.Settings = append(a.Settings, astSetting{Key: s.Key, Value: s.Value})
	}
	for _, s := range d.Sections {
		sec := astSection{Name: s.Name, Kind: s.Kind, Items: []ItemSnap{}}
		for _, n := range s.Nodes {
			switch n := n.(type) {
			case *Item:
				sec.Items = append(sec.Items, snapOf(n))
			case *Para:
				for _, l := range n.Lines {
					sec.Notes = append(sec.Notes, l)
				}
			}
		}
		a.Sections = append(a.Sections, sec)
	}
	return json.MarshalIndent(a, "", "  ")
}

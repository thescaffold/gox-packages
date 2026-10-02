package spec

import "encoding/json"

// Analysis is everything an editor needs after each keystroke, in one call:
// the canonical text, the notes (parse, validate, secrets) and the outline.
// The browser build (wasm/) and the server call this same function, so the two
// can never disagree about what a document means.
type Analysis struct {
	Canonical   string        `json:"canonical"`
	Diagnostics []Note        `json:"diagnostics"`
	Outline     []OutlineItem `json:"outline"`
	Title       string        `json:"title"`
}

// Note is a Diagnostic as the editor reads it: lower-case names, the severity as a word.
type Note struct {
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Fix      *Edit  `json:"fix,omitempty"`
}

// Notes converts diagnostics to the form the editor reads.
func Notes(ds []Diagnostic) []Note {
	out := make([]Note, len(ds))
	for i, d := range ds {
		out[i] = Note{d.Line, d.Col, d.Severity.String(), d.Code, d.Message, d.Fix}
	}
	return out
}

// Analyze parses text without changing the caller's copy, canonicalizes a copy of it, and reports.
func Analyze(text string) Analysis {
	doc, diags := Parse(text)
	diags = append(diags, Validate(doc)...)
	diags = append(diags, ScanSecrets(text)...)
	Canonicalize(doc, nil)
	out := Outline(doc)
	if out == nil {
		out = []OutlineItem{}
	}
	return Analysis{Canonical: Print(doc), Diagnostics: Notes(diags), Outline: out, Title: doc.Title}
}

// AnalyzeJSON is Analyze as JSON, the form that crosses the WebAssembly boundary.
func AnalyzeJSON(text string) []byte {
	b, err := json.Marshal(Analyze(text))
	if err != nil {
		// Analysis holds only strings, numbers and slices of them.
		panic(err)
	}
	return b
}

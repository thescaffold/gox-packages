package spec

import "fmt"

// Severity says how much a Diagnostic matters.
type Severity int

const (
	// Info: nothing is wrong, the parser is telling you what it did.
	Info Severity = iota
	// Warning: something is probably not what was meant; nothing is lost.
	Warning
	// Error: the file cannot be used as it is.
	Error
)

func (s Severity) String() string {
	switch s {
	case Info:
		return "info"
	case Warning:
		return "warning"
	}
	return "error"
}

// Edit replaces the text from column Col up to (not including) EndCol of Line
// with Text (both columns 1-based, in bytes). Col == EndCol inserts.
type Edit struct {
	Line   int    `json:"line"`
	Col    int    `json:"col"`
	EndCol int    `json:"endCol"`
	Text   string `json:"text"`
}

// Diagnostic is a plain-language note about a place in the text.
type Diagnostic struct {
	Line, Col int
	Severity  Severity
	// Code is a short stable name for tests and tools ("raw-text", "id-unclosed").
	Code    string
	Message string
	// Fix, when set, is a safe edit that resolves the diagnostic.
	Fix *Edit
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%d:%d %s %s: %s", d.Line, d.Col, d.Severity, d.Code, d.Message)
}

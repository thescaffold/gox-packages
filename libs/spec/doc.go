// Package spec reads, prints and normalises OSpec, the structured-Markdown
// language a system's specification is written in (TRD §6.18, Appendix E).
//
// Parsing is forgiving and lossless in spirit: text the parser does not
// understand is kept as a Raw node and reported as a plain-language Diagnostic,
// never dropped. Print gives the canonical form of a document and
// Print(Parse(Print(Parse(x)))) == Print(Parse(x)) for every input. Canonicalize
// gives every item a stable {#id}, resolves duplicate ids and keeps references
// right when an item is renamed.
//
// The package uses only the standard library so it can also be compiled to
// WebAssembly.
package spec

// Doc is a parsed OSpec document.
type Doc struct {
	// Version is the value of the "ospec:" setting ("1"), empty when absent.
	Version string
	// Title and Summary are the "# " and "> " lines.
	Title    string
	HasTitle bool
	Summary  string
	// Settings are the other "key: value" lines of the header (platform, market,
	// currency, language, and anything an extension adds), in order.
	Settings []Setting
	// Pre holds anything before the title (a comment, say); Intro holds text
	// between the header and the first section.
	Pre, Intro []Node
	Sections   []*Section

	tix *titleIndex // see titles()
}

// Setting is one "key: value" line in the header.
type Setting struct {
	Key, Value string
	Line       int
}

// Section is a "## Name" heading and what follows it.
type Section struct {
	// Name is the heading text; for a known section it is the canonical spelling.
	Name string
	// Kind is the kind of item the section holds ("feature" for Features) or
	// "note" for Notes and for any heading outside the vocabulary.
	Kind  string
	Known bool
	Line  int
	// Nodes are items, free text (Para, in note sections), comments and Raw text.
	Nodes []Node
}

// Node is anything that can appear in a section or an item's body.
type Node interface{ nodeLine() int }

// Para is free text: the lines are kept as written (trailing space removed).
type Para struct {
	Lines []string
	Line  int
}

// Raw is text the parser did not understand, kept as written. Fenced code
// blocks are Raw too (without a diagnostic).
type Raw struct {
	Lines []string
	Line  int
}

// Comment is an HTML comment, kept and otherwise ignored.
type Comment struct {
	Lines []string
	Line  int
}

// Prop is a "key: value" line inside an item.
type Prop struct {
	Key, Value string
	Line       int
}

// Box is the checkbox state of a list entry.
type Box int

const (
	BoxNone Box = iota
	BoxOpen
	BoxDone
)

// Entry is one bullet of a ListProp.
type Entry struct {
	Text string
	ID   string
	Box  Box
	Line int
}

// ListProp is a bare "key:" line followed by bullets ("must:", "done when:").
type ListProp struct {
	Key     string
	Entries []*Entry
	Line    int
}

// Child is a bullet inside an item: a field of an entity, a nested line.
type Child struct {
	Text string
	ID   string
	// Depth is 0 for a bullet directly under the item, 1 for one nested in it.
	Depth int
	// Cont are indented lines that continue the bullet's text.
	Cont []string
	Line int
}

// Option is a "- (a) text" choice of a question.
type Option struct {
	Key  string `json:"key"`
	Text string `json:"text"`
	Line int    `json:"line,omitempty"`
}

// Style is how an item is written.
type Style int

const (
	StyleBlock    Style = iota // ### Title
	StyleBullet                // - **Name**: text
	StyleQuestion              // ? text
)

// Item is a typed, addressable part of the spec.
type Item struct {
	// Kind comes from the section: user, feature, entity, screen, part,
	// integration, rule, limit, environment, decision, question, assumption, note.
	Kind  string
	Style Style
	// Title is a block item's heading, or a bullet item's bold name.
	Title string
	// Named reports a bullet item written "- **Name**: text".
	Named bool
	// Text is a bullet item's description or a question's text.
	Text string
	ID   string
	// Cont are indented lines that continue a bullet item's text.
	Cont []string
	// Body holds a block item's properties, lists, bullets and text, and a bullet
	// item's nested bullets (as Child).
	Body []Node
	// Options and Answer belong to a question.
	Options   []Option
	Answer    string
	HasAnswer bool
	Line      int
}

func (n *Para) nodeLine() int     { return n.Line }
func (n *Raw) nodeLine() int      { return n.Line }
func (n *Comment) nodeLine() int  { return n.Line }
func (n *Prop) nodeLine() int     { return n.Line }
func (n *ListProp) nodeLine() int { return n.Line }
func (n *Child) nodeLine() int    { return n.Line }
func (n *Item) nodeLine() int     { return n.Line }

// the item kind each known section holds
var sectionKinds = map[string]string{
	"users": "user", "features": "feature", "data": "entity", "screens": "screen",
	"parts": "part", "integrations": "integration", "rules": "rule", "limits": "limit",
	"environments": "environment", "decisions": "decision", "questions": "question",
	"assumptions": "assumption", "notes": "note",
}

// the canonical spelling of each known section, in the order the language lists them
var sectionNames = []string{"Users", "Features", "Data", "Screens", "Parts", "Integrations", "Rules", "Limits", "Environments", "Decisions", "Questions", "Assumptions", "Notes"}

// lists whose entries are addressable (they get ids): the rules, acceptance
// criteria and deferrals of a feature
var identifiedLists = map[string]string{"must": "must", "done when": "criterion", "not now": "not_now"}

package core

import (
	"encoding/json"
	"fmt"
	"time"
)

// Role of a message author.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Valid reports whether r is one of the known roles.
func (r Role) Valid() bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return true
	}
	return false
}

// CacheHint marks a prompt-cache breakpoint after the block it is attached to.
// Stable content goes first with a hint; volatile content after it (TRD §6.2).
type CacheHint struct {
	// TTL is the requested lifetime, e.g. "5m" or "1h"; empty means the
	// provider default.
	TTL string `json:"ttl,omitempty"`
}

// MediaSource is binary or remote media referenced by a content block.
type MediaSource struct {
	Kind      string `json:"kind"` // "base64" or "url"
	MediaType string `json:"mediaType"`
	Data      string `json:"data,omitempty"` // when Kind == "base64"
	URL       string `json:"url,omitempty"`  // when Kind == "url"
}

// Block is one piece of message content. The set is closed: only the types in
// this file implement it, and (Un)marshalling dispatches on a "type" field.
type Block interface {
	// BlockType is the discriminator written to JSON.
	BlockType() string
}

const (
	TypeText             = "text"
	TypeImage            = "image"
	TypeDocument         = "document"
	TypeToolUse          = "tool_use"
	TypeToolResult       = "tool_result"
	TypeThinking         = "thinking"
	TypeRedactedThinking = "redacted_thinking"
)

// TextBlock is plain text.
type TextBlock struct {
	Text  string     `json:"text"`
	Cache *CacheHint `json:"cache,omitempty"`
}

// ImageBlock is an image.
type ImageBlock struct {
	Source MediaSource `json:"source"`
}

// DocumentBlock is a document such as a PDF.
type DocumentBlock struct {
	Source MediaSource `json:"source"`
	Title  string      `json:"title,omitempty"`
}

// ToolUseBlock is a model's request to call a tool. Input is the raw JSON the
// model produced; it is validated against the tool's schema before it runs.
type ToolUseBlock struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResultBlock hands a tool's result back to the model.
type ToolResultBlock struct {
	ToolUseID string  `json:"toolUseId"`
	Content   Content `json:"content"`
	IsError   bool    `json:"isError,omitempty"`
}

// ThinkingBlock is reasoning content. Signature is opaque and provider-supplied;
// it must be sent back unchanged.
type ThinkingBlock struct {
	Thinking  string `json:"thinking"`
	Signature string `json:"signature,omitempty"`
}

// RedactedThinkingBlock is reasoning the provider withheld. Data is opaque and
// must be sent back unchanged for the next turn to be accepted.
type RedactedThinkingBlock struct {
	Data string `json:"data"`
}

func (TextBlock) BlockType() string             { return TypeText }
func (ImageBlock) BlockType() string            { return TypeImage }
func (DocumentBlock) BlockType() string         { return TypeDocument }
func (ToolUseBlock) BlockType() string          { return TypeToolUse }
func (ToolResultBlock) BlockType() string       { return TypeToolResult }
func (ThinkingBlock) BlockType() string         { return TypeThinking }
func (RedactedThinkingBlock) BlockType() string { return TypeRedactedThinking }

// Content is an ordered list of blocks that round-trips through JSON.
type Content []Block

// Text builds a Content holding one text block.
func Text(s string) Content { return Content{TextBlock{Text: s}} }

// PlainText concatenates the text of every text block, in order.
func (c Content) PlainText() string {
	var out string
	for _, b := range c {
		if t, ok := b.(TextBlock); ok {
			out += t.Text
		}
	}
	return out
}

// ToolUses returns the tool_use blocks, in order.
func (c Content) ToolUses() []ToolUseBlock {
	var out []ToolUseBlock
	for _, b := range c {
		if t, ok := b.(ToolUseBlock); ok {
			out = append(out, t)
		}
	}
	return out
}

// MarshalJSON writes each block with its "type" discriminator. A nil Content
// is an empty array, never null, so a stored message always has a list.
func (c Content) MarshalJSON() ([]byte, error) {
	out := make([]json.RawMessage, 0, len(c))
	for i, b := range c {
		if b == nil {
			return nil, fmt.Errorf("core: content block %d is nil", i)
		}
		raw, err := marshalBlock(b)
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return json.Marshal(out)
}

func marshalBlock(b Block) (json.RawMessage, error) {
	body, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	t, _ := json.Marshal(b.BlockType())
	m["type"] = t
	return json.Marshal(m)
}

// UnmarshalJSON reads a block list, rejecting a block with no or an unknown
// type rather than dropping it: silently losing a thinking or tool_use block
// would corrupt an append-only history.
func (c *Content) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return err
	}
	out := make(Content, 0, len(raws))
	for i, raw := range raws {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return fmt.Errorf("core: content block %d: %w", i, err)
		}
		var b Block
		var err error
		switch head.Type {
		case TypeText:
			b = decode[TextBlock](raw, &err)
		case TypeImage:
			b = decode[ImageBlock](raw, &err)
		case TypeDocument:
			b = decode[DocumentBlock](raw, &err)
		case TypeToolUse:
			b = decode[ToolUseBlock](raw, &err)
		case TypeToolResult:
			b = decode[ToolResultBlock](raw, &err)
		case TypeThinking:
			b = decode[ThinkingBlock](raw, &err)
		case TypeRedactedThinking:
			b = decode[RedactedThinkingBlock](raw, &err)
		case "":
			return fmt.Errorf("core: content block %d has no type", i)
		default:
			return fmt.Errorf("core: content block %d has unknown type %q", i, head.Type)
		}
		if err != nil {
			return fmt.Errorf("core: content block %d (%s): %w", i, head.Type, err)
		}
		out = append(out, b)
	}
	*c = out
	return nil
}

func decode[T Block](raw json.RawMessage, errp *error) Block {
	var v T
	*errp = json.Unmarshal(raw, &v)
	return v
}

// Message is one entry of a conversation.
type Message struct {
	Role    Role    `json:"role"`
	Content Content `json:"content"`
	// Name is an optional display or author name (a tool name, a participant).
	Name string `json:"name,omitempty"`
	// At is set by stores; callers need not supply it.
	At time.Time `json:"at,omitempty"`
}

// UserText, AssistantText build single-text-block messages.
func UserText(s string) Message      { return Message{Role: RoleUser, Content: Text(s)} }
func AssistantText(s string) Message { return Message{Role: RoleAssistant, Content: Text(s)} }

// Validate checks a message is well formed enough to store: a known role, at
// least one block, and every tool_use / tool_result carrying its id.
func (m Message) Validate() error {
	if !m.Role.Valid() {
		return fmt.Errorf("core: unknown role %q", m.Role)
	}
	if len(m.Content) == 0 {
		return fmt.Errorf("core: message has no content")
	}
	for i, b := range m.Content {
		switch t := b.(type) {
		case nil:
			return fmt.Errorf("core: content block %d is nil", i)
		case ToolUseBlock:
			if t.ID == "" || t.Name == "" {
				return fmt.Errorf("core: tool_use block %d needs an id and a name", i)
			}
		case ToolResultBlock:
			if t.ToolUseID == "" {
				return fmt.Errorf("core: tool_result block %d needs a toolUseId", i)
			}
		}
	}
	return nil
}

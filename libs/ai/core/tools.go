package core

import "encoding/json"

// ToolDef describes a tool to a model. InputSchema is a JSON Schema object so
// it maps onto every provider's function-calling format and onto MCP's
// tools/list.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
	// Strict asks the provider to guarantee the input matches the schema, where
	// it supports that. Drivers set it on every tool they can (TRD §6.2).
	Strict bool `json:"strict,omitempty"`
	// EagerInputStreaming asks for tool input to stream as it is generated (for
	// file-writing tools). The provider then stops validating the input, so the
	// driver must validate it against InputSchema before anything runs.
	EagerInputStreaming bool `json:"eagerInputStreaming,omitempty"`
}

// ToolCall is a model's request to run a tool, correlated to its result by ID.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult is what a tool returned.
type ToolResult struct {
	ToolCallID string  `json:"toolCallId"`
	Content    Content `json:"content"`
	IsError    bool    `json:"isError,omitempty"`
}

// Block converts the result into the block handed back to the model.
func (r ToolResult) Block() ToolResultBlock {
	return ToolResultBlock{ToolUseID: r.ToolCallID, Content: r.Content, IsError: r.IsError}
}

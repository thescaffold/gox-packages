package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/thescaffold/gox-packages/libs/ai/core"
)

// EventType discriminates a StreamEvent.
type EventType string

const (
	EventMessageStart  EventType = "message_start"
	EventBlockStart    EventType = "block_start"
	EventTextDelta     EventType = "text_delta"
	EventThinkingDelta EventType = "thinking_delta"
	EventSignature     EventType = "signature"
	EventToolInput     EventType = "tool_input_delta"
	EventBlockStop     EventType = "block_stop"
	EventMessageDelta  EventType = "message_delta"
	// EventMessageStop is terminal and carries the fully assembled response.
	EventMessageStop EventType = "message_stop"
	// EventError is terminal.
	EventError EventType = "error"
)

// StreamEvent is one increment of a streamed call. Which fields are set
// depends on Type.
type StreamEvent struct {
	Type  EventType
	Model string // message_start
	Index int    // block_* and *_delta: which content block

	BlockType string // block_start: core.TypeText, TypeThinking, TypeRedactedThinking or TypeToolUse
	ToolID    string // block_start for a tool_use
	ToolName  string // block_start for a tool_use
	Data      string // block_start for redacted_thinking

	Text        string // text_delta, thinking_delta, signature
	PartialJSON string // tool_input_delta

	StopReason core.StopReason // message_delta
	Usage      *core.Usage     // message_delta: usage so far (cumulative)

	Response *core.ChatResponse // message_stop
	Err      error              // error
}

// Terminal reports whether no event follows this one.
func (e StreamEvent) Terminal() bool {
	return e.Type == EventMessageStop || e.Type == EventError
}

// Accumulator folds stream events into the ChatResponse a non-streaming call
// would have returned, so every driver's Stream and Chat agree by
// construction. It is not safe for concurrent use.
type Accumulator struct {
	model      string
	blocks     map[int]*partial
	order      []int
	stop       core.StopReason
	usage      core.Usage
	providerID string
}

type partial struct {
	typ       string
	text      string
	signature string
	data      string
	toolID    string
	toolName  string
	json      []byte
}

// SetProviderID records the provider's response id for the final response.
func (a *Accumulator) SetProviderID(id string) { a.providerID = id }

// Add folds one event in. A terminal event is not an input: use Response once
// the stream has ended.
func (a *Accumulator) Add(e StreamEvent) error {
	switch e.Type {
	case EventMessageStart:
		a.model = e.Model
	case EventBlockStart:
		if a.blocks == nil {
			a.blocks = map[int]*partial{}
		}
		if _, dup := a.blocks[e.Index]; dup {
			return fmt.Errorf("llm: block %d started twice", e.Index)
		}
		switch e.BlockType {
		case core.TypeText, core.TypeThinking, core.TypeRedactedThinking, core.TypeToolUse:
		default:
			return fmt.Errorf("llm: block %d has unsupported type %q", e.Index, e.BlockType)
		}
		a.blocks[e.Index] = &partial{typ: e.BlockType, toolID: e.ToolID, toolName: e.ToolName, data: e.Data}
		a.order = append(a.order, e.Index)
	case EventTextDelta, EventThinkingDelta, EventSignature, EventToolInput:
		p, ok := a.blocks[e.Index]
		if !ok {
			return fmt.Errorf("llm: %s for unknown block %d", e.Type, e.Index)
		}
		switch e.Type {
		case EventTextDelta:
			if p.typ != core.TypeText {
				return fmt.Errorf("llm: text_delta on a %s block", p.typ)
			}
			p.text += e.Text
		case EventThinkingDelta:
			if p.typ != core.TypeThinking {
				return fmt.Errorf("llm: thinking_delta on a %s block", p.typ)
			}
			p.text += e.Text
		case EventSignature:
			if p.typ != core.TypeThinking {
				return fmt.Errorf("llm: signature on a %s block", p.typ)
			}
			p.signature += e.Text
		case EventToolInput:
			if p.typ != core.TypeToolUse {
				return fmt.Errorf("llm: tool_input_delta on a %s block", p.typ)
			}
			p.json = append(p.json, e.PartialJSON...)
		}
	case EventBlockStop:
		if _, ok := a.blocks[e.Index]; !ok {
			return fmt.Errorf("llm: block_stop for unknown block %d", e.Index)
		}
	case EventMessageDelta:
		if e.StopReason != "" {
			a.stop = e.StopReason
		}
		if e.Usage != nil {
			a.usage = *e.Usage
		}
	default:
		return fmt.Errorf("llm: %q is not an accumulable event", e.Type)
	}
	return nil
}

// Response builds the assistant message. A tool_use whose accumulated input is
// not valid JSON is an error here: the caller must not run a tool on input it
// cannot parse. An empty input becomes {}.
func (a *Accumulator) Response() (*core.ChatResponse, error) {
	content := make(core.Content, 0, len(a.order))
	for _, idx := range a.order {
		p := a.blocks[idx]
		switch p.typ {
		case core.TypeText:
			content = append(content, core.TextBlock{Text: p.text})
		case core.TypeThinking:
			content = append(content, core.ThinkingBlock{Thinking: p.text, Signature: p.signature})
		case core.TypeRedactedThinking:
			content = append(content, core.RedactedThinkingBlock{Data: p.data})
		case core.TypeToolUse:
			in := p.json
			if len(in) == 0 {
				in = []byte("{}")
			}
			if !json.Valid(in) {
				return nil, fmt.Errorf("llm: tool_use %q (%s) input is not valid JSON", p.toolName, p.toolID)
			}
			content = append(content, core.ToolUseBlock{ID: p.toolID, Name: p.toolName, Input: json.RawMessage(in)})
		}
	}
	stop := a.stop
	if stop == "" {
		return nil, errors.New("llm: stream ended without a stop reason")
	}
	return &core.ChatResponse{
		Message:    core.Message{Role: core.RoleAssistant, Content: content},
		StopReason: stop,
		Usage:      a.usage,
		Model:      a.model,
		ProviderID: a.providerID,
	}, nil
}

// Collect drains a stream to its terminal event and returns the response, or
// the stream's error. A channel that closes without a terminal event is an
// error: the contract says one always arrives.
func Collect(ctx context.Context, ch <-chan StreamEvent) (*core.ChatResponse, error) {
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return nil, errors.New("llm: stream closed without a terminal event")
			}
			switch e.Type {
			case EventMessageStop:
				if e.Response == nil {
					return nil, errors.New("llm: message_stop without a response")
				}
				return e.Response, nil
			case EventError:
				if e.Err == nil {
					return nil, errors.New("llm: error event without an error")
				}
				return nil, e.Err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

// buildBody renders a request as Responses API JSON. It is stateless
// (store: false): reasoning is replayed from each item's encrypted_content,
// which the driver keeps in the thinking block's signature.
func buildBody(info llm.ModelInfo, r llm.ChatRequest) (map[string]any, error) {
	body := map[string]any{"model": r.Model, "stream": true, "store": false}

	input := make([]any, 0, len(r.Messages))
	for i, m := range r.Messages {
		items, err := convertMessage(m)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		input = append(input, items...)
	}
	body["input"] = input

	if len(r.System) > 0 {
		parts := make([]string, 0, len(r.System))
		for _, s := range r.System {
			parts = append(parts, s.Text)
		}
		body["instructions"] = strings.Join(parts, "\n\n")
	}

	if len(r.Tools) > 0 {
		tools := make([]any, 0, len(r.Tools))
		for _, t := range r.Tools {
			// strict mode needs every property required and no extras, which a
			// general schema doesn't promise; inputs are validated by the caller
			td := map[string]any{"type": "function", "name": t.Name, "parameters": t.InputSchema, "strict": false}
			if t.Description != "" {
				td["description"] = t.Description
			}
			tools = append(tools, td)
		}
		body["tools"] = tools
		switch r.ToolChoice.Mode {
		case llm.ToolNone:
			body["tool_choice"] = "none"
		case llm.ToolAny:
			body["tool_choice"] = "required"
		case llm.ToolOne:
			body["tool_choice"] = map[string]any{"type": "function", "name": r.ToolChoice.Name}
		default:
			body["tool_choice"] = "auto"
		}
	}

	if r.MaxTokens > 0 {
		body["max_output_tokens"] = r.MaxTokens
	}

	if info.SupportsThinking {
		reasoning := map[string]any{}
		if r.Thinking != llm.ThinkingOff {
			reasoning["summary"] = "auto"
		}
		if info.SupportsEffort {
			switch r.Effort {
			case llm.EffortLow, llm.EffortMedium, llm.EffortHigh:
				reasoning["effort"] = string(r.Effort)
			case llm.EffortXHigh, llm.EffortMax:
				reasoning["effort"] = "high"
			default:
				if r.Thinking == llm.ThinkingOff {
					reasoning["effort"] = "low" // the least reasoning every family accepts
				}
			}
		}
		if len(reasoning) > 0 {
			body["reasoning"] = reasoning
		}
		body["include"] = []string{"reasoning.encrypted_content"}
	}
	return body, nil
}

// packSignature keeps a reasoning item's id and encrypted content in one opaque
// string; unpackSignature splits it again for the replay.
func packSignature(id, encrypted string) string {
	if id == "" {
		return encrypted
	}
	return id + "|" + encrypted
}

func unpackSignature(sig string) (id, encrypted string) {
	if strings.HasPrefix(sig, "rs_") {
		if i := strings.IndexByte(sig, '|'); i > 0 {
			return sig[:i], sig[i+1:]
		}
	}
	return "", sig
}

func convertMessage(m core.Message) ([]any, error) {
	if m.Role == core.RoleAssistant {
		return convertAssistant(m.Content)
	}
	var items []any
	var parts []any
	for _, b := range m.Content {
		switch v := b.(type) {
		case core.ToolResultBlock:
			items = append(items, map[string]any{"type": "function_call_output", "call_id": v.ToolUseID, "output": resultText(v)})
		default:
			p, err := userPart(b)
			if err != nil {
				return nil, err
			}
			parts = append(parts, p)
		}
	}
	if len(parts) > 0 {
		items = append(items, map[string]any{"role": "user", "content": parts})
	}
	return items, nil
}

// resultText renders a tool result as the string the API takes; an error is
// marked so the model can tell.
func resultText(v core.ToolResultBlock) string {
	t := v.Content.PlainText()
	if v.IsError {
		return "Error: " + t
	}
	return t
}

func userPart(b core.Block) (map[string]any, error) {
	switch v := b.(type) {
	case core.TextBlock:
		return map[string]any{"type": "input_text", "text": v.Text}, nil
	case core.ImageBlock:
		switch v.Source.Kind {
		case "url":
			return map[string]any{"type": "input_image", "image_url": v.Source.URL}, nil
		case "base64":
			return map[string]any{"type": "input_image", "image_url": "data:" + v.Source.MediaType + ";base64," + v.Source.Data}, nil
		}
		return nil, fmt.Errorf("unknown media source kind %q", v.Source.Kind)
	case core.DocumentBlock:
		name := v.Title
		if name == "" {
			name = "document"
		}
		switch v.Source.Kind {
		case "url":
			return map[string]any{"type": "input_file", "file_url": v.Source.URL}, nil
		case "base64":
			return map[string]any{"type": "input_file", "filename": name, "file_data": "data:" + v.Source.MediaType + ";base64," + v.Source.Data}, nil
		}
		return nil, fmt.Errorf("unknown media source kind %q", v.Source.Kind)
	}
	return nil, fmt.Errorf("unsupported block %T in a user message", b)
}

func convertAssistant(c core.Content) ([]any, error) {
	var items []any
	var text []any
	flush := func() {
		if len(text) > 0 {
			items = append(items, map[string]any{"role": "assistant", "content": text})
			text = nil
		}
	}
	for _, b := range c {
		switch v := b.(type) {
		case core.TextBlock:
			text = append(text, map[string]any{"type": "output_text", "text": v.Text})
		case core.ThinkingBlock:
			flush()
			if v.Signature == "" {
				continue // reasoning from another provider can't be replayed here
			}
			id, enc := unpackSignature(v.Signature)
			item := map[string]any{"type": "reasoning", "encrypted_content": enc,
				"summary": []any{map[string]any{"type": "summary_text", "text": v.Thinking}}}
			if id != "" {
				item["id"] = id
			}
			items = append(items, item)
		case core.RedactedThinkingBlock:
			// Anthropic's; nothing OpenAI can use
		case core.ToolUseBlock:
			flush()
			in := v.Input
			if len(in) == 0 {
				in = json.RawMessage("{}")
			}
			items = append(items, map[string]any{"type": "function_call", "call_id": v.ID, "name": v.Name, "arguments": string(in)})
		default:
			return nil, fmt.Errorf("unsupported block %T in an assistant message", b)
		}
	}
	flush()
	return items, nil
}

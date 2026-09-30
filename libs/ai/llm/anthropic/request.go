package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

// buildBody renders a request as the Messages API JSON. It is built by hand,
// not from SDK structs, so every field sent is explicit and testable: nothing
// is sent that TRD §6.2 forbids (no budget_tokens, no sampling fields).
//
// forCount renders the count_tokens shape (no max_tokens, stream, thinking or
// output_config).
func buildBody(info llm.ModelInfo, r llm.ChatRequest, forCount bool) (map[string]any, error) {
	body := map[string]any{"model": r.Model}

	msgs := make([]any, 0, len(r.Messages))
	for i, m := range r.Messages {
		conv, err := convertMessage(m)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		msgs = append(msgs, conv)
	}
	body["messages"] = msgs

	if len(r.System) > 0 {
		sys := make([]any, 0, len(r.System))
		for _, s := range r.System {
			b := map[string]any{"type": "text", "text": s.Text}
			if s.Cache != nil {
				b["cache_control"] = cacheControl(s.Cache)
			}
			sys = append(sys, b)
		}
		body["system"] = sys
	}

	if len(r.Tools) > 0 {
		tools := make([]any, 0, len(r.Tools))
		for _, t := range r.Tools {
			td := map[string]any{"name": t.Name, "input_schema": t.InputSchema}
			if t.Description != "" {
				td["description"] = t.Description
			}
			if info.SupportsStrictTools {
				td["strict"] = true
			}
			if t.EagerInputStreaming {
				td["eager_input_streaming"] = true
			}
			tools = append(tools, td)
		}
		body["tools"] = tools
		switch r.ToolChoice.Mode {
		case llm.ToolNone:
			body["tool_choice"] = map[string]any{"type": "none"}
		case llm.ToolAny:
			body["tool_choice"] = map[string]any{"type": "any"}
		case llm.ToolOne:
			body["tool_choice"] = map[string]any{"type": "tool", "name": r.ToolChoice.Name}
		default:
			body["tool_choice"] = map[string]any{"type": "auto"}
		}
	}

	if forCount {
		return body, nil
	}

	body["stream"] = true
	max := r.MaxTokens
	if max == 0 {
		max = 16_000
		if info.MaxOutputTokens > 0 && max > info.MaxOutputTokens {
			max = info.MaxOutputTokens
		}
	}
	body["max_tokens"] = max

	if info.SupportsThinking {
		switch r.Thinking {
		case llm.ThinkingAdaptive:
			display := "omitted" // summaries only for roles whose UI shows them
			if d, ok := r.Extensions["thinking_display"].(string); ok && (d == "summarized" || d == "omitted") {
				display = d
			}
			body["thinking"] = map[string]any{"type": "adaptive", "display": display}
		case llm.ThinkingOff:
			body["thinking"] = map[string]any{"type": "disabled"}
		}
	}
	if info.SupportsEffort && r.Effort != llm.EffortDefault {
		body["output_config"] = map[string]any{"effort": string(r.Effort)}
	}
	// r.Budget (an advisory task budget) is not sent: its wire shape is not
	// verified against the API, and the ledger hold is the hard limit anyway.
	return body, nil
}

func cacheControl(h *core.CacheHint) map[string]any {
	c := map[string]any{"type": "ephemeral"}
	if h.TTL != "" {
		c["ttl"] = h.TTL
	}
	return c
}

func convertMessage(m core.Message) (map[string]any, error) {
	role := "user"
	if m.Role == core.RoleAssistant {
		role = "assistant"
	}
	blocks, err := convertBlocks(m.Content)
	if err != nil {
		return nil, err
	}
	return map[string]any{"role": role, "content": blocks}, nil
}

func convertBlocks(c core.Content) ([]any, error) {
	out := make([]any, 0, len(c))
	for _, b := range c {
		switch v := b.(type) {
		case core.TextBlock:
			blk := map[string]any{"type": "text", "text": v.Text}
			if v.Cache != nil {
				blk["cache_control"] = cacheControl(v.Cache)
			}
			out = append(out, blk)
		case core.ImageBlock:
			src, err := mediaSource(v.Source)
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"type": "image", "source": src})
		case core.DocumentBlock:
			src, err := mediaSource(v.Source)
			if err != nil {
				return nil, err
			}
			d := map[string]any{"type": "document", "source": src}
			if v.Title != "" {
				d["title"] = v.Title
			}
			out = append(out, d)
		case core.ToolUseBlock:
			in := v.Input
			if len(in) == 0 {
				in = json.RawMessage("{}")
			}
			out = append(out, map[string]any{"type": "tool_use", "id": v.ID, "name": v.Name, "input": in})
		case core.ToolResultBlock:
			inner, err := convertBlocks(v.Content)
			if err != nil {
				return nil, err
			}
			blk := map[string]any{"type": "tool_result", "tool_use_id": v.ToolUseID, "content": inner}
			if v.IsError {
				blk["is_error"] = true
			}
			out = append(out, blk)
		case core.ThinkingBlock:
			// replayed exactly as produced: newer models reject edited history
			out = append(out, map[string]any{"type": "thinking", "thinking": v.Thinking, "signature": v.Signature})
		case core.RedactedThinkingBlock:
			out = append(out, map[string]any{"type": "redacted_thinking", "data": v.Data})
		default:
			return nil, fmt.Errorf("unsupported block %T", b)
		}
	}
	return out, nil
}

func mediaSource(s core.MediaSource) (map[string]any, error) {
	switch s.Kind {
	case "base64":
		return map[string]any{"type": "base64", "media_type": s.MediaType, "data": s.Data}, nil
	case "url":
		return map[string]any{"type": "url", "url": s.URL}, nil
	}
	return nil, fmt.Errorf("unknown media source kind %q", s.Kind)
}

package gemini

import (
	"encoding/json"
	"fmt"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

// buildBody renders a request as generateContent JSON. forCount renders only
// what countTokens counts (no generation config).
func buildBody(info llm.ModelInfo, r llm.ChatRequest, forCount bool) (map[string]any, error) {
	names := map[string]string{} // tool call id → function name, for the results
	for _, m := range r.Messages {
		for _, tu := range m.Content.ToolUses() {
			names[tu.ID] = tu.Name
		}
	}
	contents := make([]any, 0, len(r.Messages))
	for i, m := range r.Messages {
		parts, err := convertParts(m, names)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		if len(parts) == 0 {
			continue
		}
		role := "user"
		if m.Role == core.RoleAssistant {
			role = "model"
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	body := map[string]any{"contents": contents}

	if len(r.System) > 0 {
		parts := make([]any, 0, len(r.System))
		for _, s := range r.System {
			parts = append(parts, map[string]any{"text": s.Text})
		}
		body["systemInstruction"] = map[string]any{"parts": parts}
	}

	if len(r.Tools) > 0 {
		decls := make([]any, 0, len(r.Tools))
		for _, t := range r.Tools {
			// parametersJsonSchema takes full JSON Schema; "parameters" takes only an OpenAPI subset
			d := map[string]any{"name": t.Name, "parametersJsonSchema": t.InputSchema}
			if t.Description != "" {
				d["description"] = t.Description
			}
			decls = append(decls, d)
		}
		body["tools"] = []any{map[string]any{"functionDeclarations": decls}}
		cfg := map[string]any{"mode": "AUTO"}
		switch r.ToolChoice.Mode {
		case llm.ToolNone:
			cfg["mode"] = "NONE"
		case llm.ToolAny:
			cfg["mode"] = "ANY"
		case llm.ToolOne:
			cfg["mode"] = "ANY"
			cfg["allowedFunctionNames"] = []string{r.ToolChoice.Name}
		}
		body["toolConfig"] = map[string]any{"functionCallingConfig": cfg}
	}
	if forCount {
		return body, nil
	}

	gen := map[string]any{}
	if r.MaxTokens > 0 {
		gen["maxOutputTokens"] = r.MaxTokens
	}
	if info.SupportsThinking {
		th := map[string]any{"includeThoughts": r.Thinking != llm.ThinkingOff}
		switch {
		case r.Thinking == llm.ThinkingOff && canDisableThinking(r.Model):
			th["thinkingBudget"] = 0
		case usesThinkingLevel(r.Model) && info.SupportsEffort && r.Effort != llm.EffortDefault:
			level := "high"
			if r.Effort == llm.EffortLow {
				level = "low"
			}
			th["thinkingLevel"] = level
		case !usesThinkingLevel(r.Model) && r.Thinking == llm.ThinkingAdaptive:
			th["thinkingBudget"] = -1 // dynamic: the model decides
		}
		gen["thinkingConfig"] = th
	}
	if len(gen) > 0 {
		body["generationConfig"] = gen
	}
	return body, nil
}

// convertParts renders one message. A thinking block with text is a thought
// part; one without text carries only a signature, which belongs to the part
// after it (or, at the end of the message, the part before).
func convertParts(m core.Message, names map[string]string) ([]any, error) {
	var parts []map[string]any
	pending := ""
	add := func(p map[string]any) {
		if pending != "" {
			p["thoughtSignature"] = pending
			pending = ""
		}
		parts = append(parts, p)
	}
	for _, b := range m.Content {
		switch v := b.(type) {
		case core.TextBlock:
			add(map[string]any{"text": v.Text})
		case core.ThinkingBlock:
			if v.Thinking == "" {
				pending = v.Signature
				continue
			}
			p := map[string]any{"text": v.Thinking, "thought": true}
			if v.Signature != "" {
				p["thoughtSignature"] = v.Signature
			}
			add(p)
		case core.RedactedThinkingBlock:
			// Anthropic's; nothing Gemini can use
		case core.ToolUseBlock:
			var args any = map[string]any{}
			if len(v.Input) > 0 {
				if err := json.Unmarshal(v.Input, &args); err != nil {
					return nil, fmt.Errorf("tool call %s input: %w", v.ID, err)
				}
			}
			add(map[string]any{"functionCall": map[string]any{"id": v.ID, "name": v.Name, "args": args}})
		case core.ToolResultBlock:
			name := names[v.ToolUseID]
			key := "result"
			if v.IsError {
				key = "error"
			}
			add(map[string]any{"functionResponse": map[string]any{"id": v.ToolUseID, "name": name, "response": map[string]any{key: v.Content.PlainText()}}})
		case core.ImageBlock:
			p, err := media(v.Source)
			if err != nil {
				return nil, err
			}
			add(p)
		case core.DocumentBlock:
			p, err := media(v.Source)
			if err != nil {
				return nil, err
			}
			add(p)
		default:
			return nil, fmt.Errorf("unsupported block %T", b)
		}
	}
	if pending != "" && len(parts) > 0 {
		parts[len(parts)-1]["thoughtSignature"] = pending
	}
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = p
	}
	return out, nil
}

func media(s core.MediaSource) (map[string]any, error) {
	switch s.Kind {
	case "base64":
		return map[string]any{"inlineData": map[string]any{"mimeType": s.MediaType, "data": s.Data}}, nil
	case "url":
		fd := map[string]any{"fileUri": s.URL}
		if s.MediaType != "" {
			fd["mimeType"] = s.MediaType
		}
		return map[string]any{"fileData": fd}, nil
	}
	return nil, fmt.Errorf("unknown media source kind %q", s.Kind)
}

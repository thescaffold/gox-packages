package llm

import (
	"fmt"

	"github.com/thescaffold/gox-packages/libs/ai/core"
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, a...))
}

// ValidateRequest is the check every driver runs before it makes a call, so a
// bad request fails the same way on every provider and costs nothing. info is
// the model the request names.
func ValidateRequest(info ModelInfo, r ChatRequest) error {
	if r.Model == "" {
		return invalid("model is required")
	}
	if info.ID != "" && info.ID != r.Model {
		return invalid("request names %q but capabilities are for %q", r.Model, info.ID)
	}
	if len(r.Messages) == 0 {
		return invalid("no messages")
	}
	if !r.Effort.Valid() {
		return invalid("unknown effort %q", r.Effort)
	}
	switch r.Thinking {
	case ThinkingAdaptive, ThinkingOff, ThinkingProviderDefault:
	default:
		return invalid("unknown thinking mode %q", r.Thinking)
	}
	if r.MaxTokens < 0 {
		return invalid("maxTokens is negative")
	}
	if info.MaxOutputTokens > 0 && r.MaxTokens > info.MaxOutputTokens {
		return invalid("maxTokens %d exceeds the model's %d", r.MaxTokens, info.MaxOutputTokens)
	}
	if r.Budget != nil && r.Budget.TotalTokens <= 0 {
		return invalid("task budget must be positive")
	}

	for i, s := range r.System {
		if s.Text == "" {
			return invalid("system block %d is empty", i)
		}
	}

	tools := map[string]bool{}
	for i, t := range r.Tools {
		if t.Name == "" {
			return invalid("tool %d has no name", i)
		}
		if tools[t.Name] {
			return invalid("duplicate tool %q", t.Name)
		}
		if t.InputSchema == nil {
			return invalid("tool %q has no input schema", t.Name)
		}
		tools[t.Name] = true
	}
	if len(tools) > 0 && !info.SupportsTools && info.ID != "" {
		return invalid("model %s does not support tools", info.ID)
	}

	switch r.ToolChoice.Mode {
	case "", ToolAuto, ToolNone:
	case ToolAny, ToolOne:
		if !info.SupportsForcedToolChoice {
			return invalid("model %s does not support forced tool choice (%s); use auto with an instruction", r.Model, r.ToolChoice.Mode)
		}
		if len(tools) == 0 {
			return invalid("tool choice %q with no tools", r.ToolChoice.Mode)
		}
		if r.ToolChoice.Mode == ToolOne && !tools[r.ToolChoice.Name] {
			return invalid("tool choice names %q, which is not among the tools", r.ToolChoice.Name)
		}
	default:
		return invalid("unknown tool mode %q", r.ToolChoice.Mode)
	}

	return validateHistory(r.Messages)
}

// validateHistory enforces what providers enforce, early and with a clear
// message: no system role inside Messages; every tool_result answers a
// tool_use made earlier; every tool_use is answered by the very next message;
// and the history ends on a turn the model can reply to.
func validateHistory(msgs []core.Message) error {
	open := map[string]bool{} // tool_use ids awaiting an answer
	for i, m := range msgs {
		if m.Role == core.RoleSystem {
			return invalid("message %d has the system role; use ChatRequest.System", i)
		}
		if err := m.Validate(); err != nil {
			return invalid("message %d: %v", i, err)
		}

		answered := map[string]bool{}
		for _, b := range m.Content {
			if tr, ok := b.(core.ToolResultBlock); ok {
				if !open[tr.ToolUseID] {
					return invalid("message %d answers tool call %q that was never made or is already answered", i, tr.ToolUseID)
				}
				if answered[tr.ToolUseID] {
					return invalid("message %d answers tool call %q twice", i, tr.ToolUseID)
				}
				answered[tr.ToolUseID] = true
			}
		}
		// every call pending from the previous message must be answered now
		for id := range open {
			if !answered[id] {
				return invalid("tool call %q has no result in message %d", id, i)
			}
		}
		open = map[string]bool{}

		if m.Role == core.RoleAssistant {
			for _, tu := range m.Content.ToolUses() {
				if open[tu.ID] {
					return invalid("duplicate tool call id %q in message %d", tu.ID, i)
				}
				open[tu.ID] = true
			}
		}
	}
	last := msgs[len(msgs)-1]
	if last.Role == core.RoleAssistant {
		return invalid("history ends with an assistant message; the model has nothing to reply to")
	}
	return nil
}

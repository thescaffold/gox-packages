package core

// StopReason is why a model call ended.
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopMaxTokens StopReason = "max_tokens"
	StopToolUse   StopReason = "tool_use"
	StopSequence  StopReason = "stop_sequence"
	StopCancelled StopReason = "cancelled"
	StopError     StopReason = "error"
	// StopRefusal is a first-class outcome, not an error: the model declined.
	// The caller tries the role's fallback model, then fails the step with a
	// refusal evidence record (TRD §6.2).
	StopRefusal StopReason = "refusal"
)

// ChatResponse is one assembled model reply.
type ChatResponse struct {
	// Message is the assistant message, including any thinking blocks, which
	// must be stored and replayed unchanged.
	Message    Message    `json:"message"`
	StopReason StopReason `json:"stopReason"`
	Usage      Usage      `json:"usage"`
	// Model is the model that actually answered (it can differ from the one
	// requested after a fallback).
	Model string `json:"model"`
	// ProviderID is the provider's response id, when it gave one.
	ProviderID string `json:"providerId,omitempty"`
}

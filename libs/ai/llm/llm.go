// Package llm is the one port every model provider implements, plus what all
// drivers share so they behave identically: request validation, the stream
// event vocabulary and the accumulator that turns events into a ChatResponse
// (TRD §6.2, PLAN M1-26).
//
// Drivers are stateless adapters over a vendor SDK. They are handed their
// credentials, usage sink and cache by the host; they do not import a
// database, a vault or a ledger.
package llm

import (
	"context"
	"errors"

	"github.com/thescaffold/gox-packages/libs/ai/core"
)

// Errors a caller can test with errors.Is.
var (
	// ErrInvalidRequest: the request was rejected before any call was made.
	ErrInvalidRequest = errors.New("llm: invalid request")
	// ErrUnsupported: the driver does not implement this optional capability.
	ErrUnsupported = errors.New("llm: unsupported by this driver")
)

// ModelInfo describes a model and what it can do. Drivers self-report; the
// shared validation uses it so a driver rejects what its model cannot serve.
type ModelInfo struct {
	ID       string
	Provider string
	// ContextWindow is the maximum input in tokens.
	ContextWindow   int64
	MaxOutputTokens int64

	SupportsTools     bool
	SupportsStreaming bool
	SupportsVision    bool
	SupportsThinking  bool
	// SupportsForcedToolChoice: ToolChoice "any" and "tool" work. False for
	// Fable 5.1, where tools are called with "auto" plus an instruction.
	SupportsForcedToolChoice bool
	SupportsStrictTools      bool
	// ZeroRetentionOK is false for a model that needs provider-side retention
	// (Fable 5.1 needs 30 days), so the policy can exclude it for tenants that
	// forbid retention.
	ZeroRetentionOK bool
}

// SystemBlock is a part of the system prompt. Stable blocks go first, marked
// with a CacheHint; volatile ones after.
type SystemBlock struct {
	Text  string
	Cache *core.CacheHint
}

// ToolMode is how the model may use tools.
type ToolMode string

const (
	ToolAuto ToolMode = "auto" // the default
	ToolNone ToolMode = "none"
	ToolAny  ToolMode = "any"  // must call some tool
	ToolOne  ToolMode = "tool" // must call Name
)

// ToolChoice directs tool use. The zero value means ToolAuto.
type ToolChoice struct {
	Mode ToolMode
	Name string
}

// Effort is how hard the model works, mapped per provider. There are no
// sampling parameters in a request: current models reject them.
type Effort string

const (
	EffortDefault Effort = ""
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
	EffortXHigh   Effort = "xhigh"
	EffortMax     Effort = "max"
)

// Valid reports whether e is a known effort (the zero value is valid).
func (e Effort) Valid() bool {
	switch e {
	case EffortDefault, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax:
		return true
	}
	return false
}

// ThinkingMode controls reasoning. The zero value is Adaptive.
type ThinkingMode string

const (
	ThinkingAdaptive        ThinkingMode = ""
	ThinkingOff             ThinkingMode = "off"
	ThinkingProviderDefault ThinkingMode = "provider_default"
)

// TaskBudget is an advisory token ceiling for a whole loop, passed to
// providers that accept one. The ledger hold is the hard limit.
type TaskBudget struct {
	TotalTokens int64
}

// ChatRequest is one model call.
type ChatRequest struct {
	Model string
	// System is separate from Messages; a system-role message is rejected.
	System []SystemBlock
	// Messages are append-only by contract: a driver must not reorder, edit or
	// drop any, and must not mutate the slice it was given.
	Messages   []core.Message
	Tools      []core.ToolDef
	ToolChoice ToolChoice
	MaxTokens  int64
	Effort     Effort
	Thinking   ThinkingMode
	Budget     *TaskBudget
	// Extensions are provider-specific knobs no caller may depend on.
	Extensions map[string]any
}

// ProviderDriver is implemented once per provider.
type ProviderDriver interface {
	// Provider is the stable key, e.g. "anthropic".
	Provider() string
	// Models lists what this driver instance can serve.
	Models(ctx context.Context) ([]ModelInfo, error)
	// Chat is one completed call. A refusal is a normal outcome
	// (StopRefusal), not an error; a failed call returns a *core.ProviderError.
	Chat(ctx context.Context, r ChatRequest) (*core.ChatResponse, error)
	// Stream is the same call as events. The channel carries exactly one
	// terminal event (message_stop or error) and is then closed, also when ctx
	// is cancelled. A request rejected up front is returned as an error and no
	// channel is opened.
	Stream(ctx context.Context, r ChatRequest) (<-chan StreamEvent, error)
	// CountTokens is best effort and optional: return ErrUnsupported if the
	// provider cannot.
	CountTokens(ctx context.Context, r ChatRequest) (int64, error)
}

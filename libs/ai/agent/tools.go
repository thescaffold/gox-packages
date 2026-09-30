package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/prompt"
)

// Tool is one capability the model can call.
type Tool interface {
	Def() core.ToolDef
	// Run executes the call. A returned error is shown to the model as a tool
	// error result; it does not end the run. Return a *PauseError to pause the run.
	Run(ctx context.Context, call core.ToolCall) (core.Content, error)
}

// ResumeSafer is implemented by tools that can be run again without harm if a
// worker died after starting them (reads, idempotent writes). A tool that does
// not implement it, or returns false, is never re-run blindly after an
// interruption: the model is told the outcome is unknown.
type ResumeSafer interface{ ResumeSafe() bool }

// Untrusted is implemented by tools whose results come from outside (files,
// web pages, issue text). Their results are wrapped and labelled as data.
type Untrusted interface{ UntrustedSource() string }

// Dispatcher is what the loop calls for tools. The registry below is the plain
// implementation; the policy hook (allow / ask / deny) wraps one.
type Dispatcher interface {
	Tools() []core.ToolDef
	// Dispatch runs a call and returns the result the model will see. An error
	// other than *PauseError is an infrastructure failure, not a tool failure:
	// tool failures are results with IsError.
	Dispatch(ctx context.Context, call core.ToolCall) (core.ToolResult, error)
	// ResumeSafe reports whether a call may be re-run after an interruption.
	ResumeSafe(name string) bool
}

// PauseError asks the loop to stop and leave the run resumable: the call that
// returned it has not happened and will be dispatched again on resume.
type PauseError struct {
	// Status is awaiting_approval or blocked_credits.
	Status core.RunStatus
	Detail string
}

func (e *PauseError) Error() string { return fmt.Sprintf("agent: paused (%s): %s", e.Status, e.Detail) }

// Tool error codes the model sees as the start of an error result.
const (
	CodeUnknownTool  = "UNKNOWN_TOOL"
	CodeInvalidJSON  = "INVALID_JSON"
	CodeInvalidInput = "INVALID_INPUT"
	CodeToolFailed   = "TOOL_FAILED"
	CodeTimeout      = "TOOL_TIMEOUT"
	CodePanic        = "TOOL_PANIC"
	CodeInterrupted  = "INTERRUPTED"
	CodeCancelled    = "CANCELLED"
	CodeNotExecuted  = "NOT_EXECUTED"
)

// ErrorResult builds the error result for a call.
func ErrorResult(callID, code, msg string) core.ToolResult {
	return core.ToolResult{ToolCallID: callID, IsError: true, Content: core.Text(code + ": " + msg)}
}

// Registry is a Dispatcher over a fixed set of tools. It validates every call's
// input against the tool's schema before running it, bounds each call with a
// timeout, recovers a panicking tool, truncates oversized results and labels
// untrusted ones. It is safe for concurrent use.
type Registry struct {
	// Timeout bounds one tool call (0 = 2 minutes).
	Timeout time.Duration
	// MaxResultChars truncates a text result (0 = 100,000).
	MaxResultChars int

	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry builds a registry; a duplicate tool name panics (a programming error).
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{tools: map[string]Tool{}}
	for _, t := range tools {
		if err := r.Add(t); err != nil {
			panic(err)
		}
	}
	return r
}

// Add registers a tool.
func (r *Registry) Add(t Tool) error {
	def := t.Def()
	if def.Name == "" || def.InputSchema == nil {
		return errors.New("agent: a tool needs a name and an input schema")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil {
		r.tools = map[string]Tool{}
	}
	if _, dup := r.tools[def.Name]; dup {
		return fmt.Errorf("agent: tool %q registered twice", def.Name)
	}
	r.tools[def.Name] = t
	return nil
}

func (r *Registry) Tools() []core.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]core.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Def())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name }) // stable order keeps the prompt cache warm
	return out
}

func (r *Registry) ResumeSafe(name string) bool {
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	rs, ok := t.(ResumeSafer)
	return ok && rs.ResumeSafe()
}

func (r *Registry) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 2 * time.Minute
}

func (r *Registry) maxChars() int {
	if r.MaxResultChars > 0 {
		return r.MaxResultChars
	}
	return 100_000
}

func (r *Registry) Dispatch(ctx context.Context, call core.ToolCall) (res core.ToolResult, err error) {
	r.mu.RLock()
	t, ok := r.tools[call.Name]
	r.mu.RUnlock()
	if !ok {
		return ErrorResult(call.ID, CodeUnknownTool, fmt.Sprintf("there is no tool named %q", call.Name)), nil
	}
	def := t.Def()
	if verr := core.ValidateInput(def.InputSchema, call.Input); verr != nil {
		code := CodeInvalidInput
		if !jsonValid(call.Input) {
			code = CodeInvalidJSON
		}
		return ErrorResult(call.ID, code, verr.Error()), nil
	}

	tctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	type outcome struct {
		c   core.Content
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- outcome{err: &panicError{fmt.Sprint(p)}}
			}
		}()
		c, err := t.Run(tctx, call)
		done <- outcome{c, err}
	}()

	var o outcome
	select {
	case o = <-done:
	case <-tctx.Done():
		if ctx.Err() != nil {
			return ErrorResult(call.ID, CodeCancelled, "the run was cancelled while the tool was running"), nil
		}
		return ErrorResult(call.ID, CodeTimeout, fmt.Sprintf("the tool did not finish within %s", r.timeout())), nil
	}
	if o.err != nil {
		var pe *PauseError
		if errors.As(o.err, &pe) {
			return core.ToolResult{}, pe
		}
		var pn *panicError
		if errors.As(o.err, &pn) {
			return ErrorResult(call.ID, CodePanic, pn.msg), nil
		}
		return ErrorResult(call.ID, CodeToolFailed, o.err.Error()), nil
	}
	c := truncate(o.c, r.maxChars())
	if u, ok := t.(Untrusted); ok {
		c = wrap(c, u.UntrustedSource())
	}
	return core.ToolResult{ToolCallID: call.ID, Content: c}, nil
}

type panicError struct{ msg string }

func (p *panicError) Error() string { return "tool panicked: " + p.msg }

func jsonValid(b []byte) bool { return json.Valid(b) }

func wrap(c core.Content, label string) core.Content {
	out := make(core.Content, 0, len(c))
	for _, b := range c {
		if tb, ok := b.(core.TextBlock); ok {
			tb.Text = prompt.WrapUntrusted(label, tb.Text)
			out = append(out, tb)
			continue
		}
		out = append(out, b)
	}
	return out
}

// truncate caps the total text of a result, telling the model it was cut.
func truncate(c core.Content, max int) core.Content {
	out := make(core.Content, 0, len(c))
	left := max
	for _, b := range c {
		tb, ok := b.(core.TextBlock)
		if !ok {
			out = append(out, b)
			continue
		}
		rs := []rune(tb.Text)
		switch {
		case left <= 0:
			continue
		case len(rs) > left:
			tb.Text = string(rs[:left]) + fmt.Sprintf("\n[truncated: the result was longer than %d characters]", max)
			left = 0
		default:
			left -= len(rs)
		}
		out = append(out, tb)
	}
	return out
}

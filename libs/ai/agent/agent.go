// Package agent is the turn loop: call the model, run the tools it asks for,
// feed the results back, and repeat until it is done or a limit stops it
// (TRD §6.2 and §6.3, PLAN M1-30).
//
// What the loop guarantees:
//
//   - History is append-only and written before it is acted on. The assistant
//     message (with any tool_use) is stored before a tool runs, and the tool
//     results are stored as one message after, so a run killed at any point can
//     be resumed from the store alone.
//   - Resuming does not repeat a tool that already completed. A call that was
//     started but has no result is re-run only if the tool says that is safe;
//     otherwise the model is told the outcome is unknown.
//   - max_tokens and refusal are checked before any tool runs: a cut-off or
//     refused turn never executes a call.
//   - Every limit is enforced between steps: steps, wall time, tokens, cost,
//     cancellation, and three identical tool calls in a row.
//   - Whatever ends the run, a tool_use is never left without its tool_result,
//     so the history stays valid for the next call.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

// Reason is why Run returned.
type Reason string

const (
	ReasonCompleted       Reason = "completed"
	ReasonCancelled       Reason = "cancelled"
	ReasonBudget          Reason = "budget_exceeded"
	ReasonLoop            Reason = "loop_detected"
	ReasonRefused         Reason = "refused"
	ReasonMaxTokens       Reason = "max_tokens"
	ReasonContextExceeded Reason = "context_exceeded"
	ReasonPaused          Reason = "paused"
	ReasonError           Reason = "error"
)

// Outcome is how a run (or this stretch of it) ended.
type Outcome struct {
	Reason Reason
	// Status is what the run's status was set to.
	Status core.RunStatus
	// Detail explains a non-completed outcome.
	Detail string
	// Final is the last assistant message when the run completed.
	Final *core.Message
	// Steps, Usage and Cost are totals over the whole run, including steps
	// done before a resume.
	Steps int
	Usage core.Usage
	Cost  core.MicroUSD
	// Budget names which limit stopped the run (ReasonBudget).
	Budget string
	// Ref is what a paused run waits for (ReasonPaused), from PauseError.Ref.
	Ref string
}

// Text is the final message's text, or "".
func (o *Outcome) Text() string {
	if o == nil || o.Final == nil {
		return ""
	}
	return o.Final.Content.PlainText()
}

// Budgets bound a run. A zero field uses its default; -1 disables it.
type Budgets struct {
	MaxSteps    int           // default 40
	MaxDuration time.Duration // default 30 minutes, measured per Run call
	MaxTokens   int64         // total input+output over the run; default unlimited
	MaxCost     core.MicroUSD // default unlimited
}

// EventType names what the loop reports to a listener.
type EventType string

const (
	EventStepStart  EventType = "step_start"
	EventModel      EventType = "model" // a llm.StreamEvent, in Stream
	EventToolStart  EventType = "tool_start"
	EventToolResult EventType = "tool_result"
	EventStepEnd    EventType = "step_end"
	EventRunEnd     EventType = "run_end"
)

// Event is one thing that happened, for live views. Listeners must not block.
type Event struct {
	Type    EventType
	RunID   string
	Step    int
	Stream  *llm.StreamEvent // EventModel
	Call    *core.ToolCall   // EventToolStart, EventToolResult
	Result  *core.ToolResult // EventToolResult
	Usage   *core.Usage      // EventStepEnd
	Cost    core.MicroUSD    // EventStepEnd: this step's cost
	Outcome *Outcome         // EventRunEnd
}

// Config wires the loop to its collaborators.
type Config struct {
	Model    Model
	Tools    Dispatcher
	Messages core.MessageStore
	Steps    core.AgentStepStore
	Calls    core.AgentToolCallStore
	// Usage receives one idempotent event per model call. Optional.
	Usage core.UsageSink
	// Cancel is checked between steps and before each tool. Optional.
	Cancel core.CancelToken
	// OnEvent listens for progress. Optional.
	OnEvent func(Event)

	Budgets Budgets
	// LoopThreshold: this many identical tool calls in a row end the run
	// (default 3; the call that would reach it is not executed).
	LoopThreshold int

	// Request defaults applied to every model call.
	MaxTokens  int64
	Effort     llm.Effort
	Thinking   llm.ThinkingMode
	Extensions map[string]any

	// Now is the clock (default time.Now).
	Now func() time.Time
}

// Input starts or resumes a run.
type Input struct {
	RunID       string
	WorkspaceID string
	System      []llm.SystemBlock
	// Message is the new user turn. Leave it empty to resume a run from its
	// stored history.
	Message *core.Message
}

// Agent runs turns.
type Agent struct{ cfg Config }

// New builds an agent, checking the wiring.
func New(cfg Config) (*Agent, error) {
	switch {
	case cfg.Model == nil:
		return nil, errors.New("agent: Model is required")
	case cfg.Tools == nil:
		return nil, errors.New("agent: Tools is required (use an empty Registry for none)")
	case cfg.Messages == nil || cfg.Steps == nil || cfg.Calls == nil:
		return nil, errors.New("agent: Messages, Steps and Calls stores are required")
	}
	return &Agent{cfg: cfg}, nil
}

func (a *Agent) now() time.Time {
	if a.cfg.Now != nil {
		return a.cfg.Now()
	}
	return time.Now()
}

func (a *Agent) emit(e Event) {
	if a.cfg.OnEvent != nil {
		a.cfg.OnEvent(e)
	}
}

func (b Budgets) steps() int {
	switch {
	case b.MaxSteps < 0:
		return 0
	case b.MaxSteps == 0:
		return 40
	}
	return b.MaxSteps
}

func (b Budgets) duration() time.Duration {
	switch {
	case b.MaxDuration < 0:
		return 0
	case b.MaxDuration == 0:
		return 30 * time.Minute
	}
	return b.MaxDuration
}

// run is the state of one Run call.
type run struct {
	a       *Agent
	in      Input
	ctx     context.Context
	started time.Time
	msgs    []core.Message
	steps   []core.StepRecord
	usage   core.Usage
	cost    core.MicroUSD
	// loop detection
	lastSig string
	repeats int
}

// Run starts a run with in.Message, or resumes it when Message is nil.
func (a *Agent) Run(ctx context.Context, in Input) (*Outcome, error) {
	if in.RunID == "" {
		return nil, errors.New("agent: RunID is required")
	}
	if a.cfg.Cancel != nil {
		var release context.CancelFunc
		ctx, release = core.Context(ctx, a.cfg.Cancel)
		defer release()
	}
	r := &run{a: a, in: in, ctx: ctx, started: a.now()}

	msgs, err := a.cfg.Messages.List(ctx, in.RunID)
	if err != nil {
		return nil, err
	}
	r.msgs = msgs
	if r.steps, err = a.cfg.Steps.List(ctx, in.RunID); err != nil {
		return nil, err
	}
	for _, s := range r.steps {
		r.usage = r.usage.Add(s.Usage)
		r.cost += s.Cost
	}

	if in.Message != nil {
		if in.Message.Role != core.RoleUser {
			return nil, errors.New("agent: the input message must be a user message")
		}
		// a new turn on a run whose last assistant message still has unanswered
		// tool calls cannot be appended to: settle those first
		if err := r.settlePending(CodeInterrupted, "the run was interrupted before this tool finished"); err != nil {
			return nil, err
		}
		if err := r.append(*in.Message); err != nil {
			return nil, err
		}
	} else if len(r.msgs) == 0 {
		return nil, errors.New("agent: nothing to resume: the run has no history and no input message")
	}

	if err := a.cfg.Steps.SetStatus(ctx, in.RunID, core.RunRunning, ""); err != nil {
		return nil, err
	}
	out := r.loop()
	out.Steps, out.Usage, out.Cost = len(r.steps), r.usage, r.cost
	// a cancelled context must not stop the final bookkeeping
	bg := context.WithoutCancel(ctx)
	if err := a.cfg.Steps.SetStatus(bg, in.RunID, out.Status, out.Detail); err != nil {
		return out, err
	}
	a.emit(Event{Type: EventRunEnd, RunID: in.RunID, Outcome: out})
	return out, nil
}

func (r *run) append(m core.Message) error {
	if err := r.a.cfg.Messages.Append(context.WithoutCancel(r.ctx), r.in.RunID, m); err != nil {
		return err
	}
	r.msgs = append(r.msgs, m)
	return nil
}

func (r *run) end(reason Reason, status core.RunStatus, detail string) *Outcome {
	return &Outcome{Reason: reason, Status: status, Detail: detail}
}

// cancelled reports whether the run was cancelled, by token or context.
func (r *run) cancelled() bool {
	if r.a.cfg.Cancel != nil && r.a.cfg.Cancel.Cancelled() {
		return true
	}
	return r.ctx.Err() != nil && !errors.Is(r.ctx.Err(), context.DeadlineExceeded)
}

func (r *run) cancelOutcome() *Outcome {
	_ = r.settlePending(CodeCancelled, "the run was cancelled before this tool ran")
	detail := "cancelled"
	if r.a.cfg.Cancel != nil && r.a.cfg.Cancel.Reason() != "" {
		detail = r.a.cfg.Cancel.Reason()
	}
	return r.end(ReasonCancelled, core.RunCancelled, detail)
}

// pending returns the tool_use blocks of the last assistant message that have
// no result after them.
func (r *run) pending() []core.ToolUseBlock {
	if len(r.msgs) == 0 {
		return nil
	}
	last := r.msgs[len(r.msgs)-1]
	if last.Role != core.RoleAssistant {
		return nil
	}
	return last.Content.ToolUses()
}

// settlePending answers every unanswered tool call with an error result, so the
// history stays valid. It does nothing when nothing is pending.
func (r *run) settlePending(code, msg string) error {
	pend := r.pending()
	if len(pend) == 0 {
		return nil
	}
	blocks := make(core.Content, 0, len(pend))
	for _, tu := range pend {
		blocks = append(blocks, ErrorResult(tu.ID, code, msg).Block())
	}
	return r.append(core.Message{Role: core.RoleUser, Content: blocks})
}

func (r *run) budgetHit() (string, bool) {
	b := r.a.cfg.Budgets
	if n := b.steps(); n > 0 && len(r.steps) >= n {
		return "steps", true
	}
	if d := b.duration(); d > 0 && r.a.now().Sub(r.started) >= d {
		return "time", true
	}
	if b.MaxTokens > 0 && r.usage.InputTokens+r.usage.OutputTokens+r.usage.CacheReadTokens+r.usage.CacheWriteTokens >= b.MaxTokens {
		return "tokens", true
	}
	if b.MaxCost > 0 && r.cost >= b.MaxCost {
		return "cost", true
	}
	return "", false
}

func (r *run) loop() *Outcome {
	for {
		// a run resumed with tool calls still pending runs them before asking the model again
		if pend := r.pending(); len(pend) > 0 {
			if out := r.runTools(pend, nil); out != nil {
				return out
			}
			continue
		}
		if last := r.msgs[len(r.msgs)-1]; last.Role == core.RoleAssistant {
			// the model already had the last word: a finished run being resumed
			m := last
			return &Outcome{Reason: ReasonCompleted, Status: core.RunSucceeded, Final: &m}
		}

		if r.cancelled() {
			return r.cancelOutcome()
		}
		if which, hit := r.budgetHit(); hit {
			o := r.end(ReasonBudget, core.RunFailed, which+" budget exceeded")
			o.Budget = which
			return o
		}

		resp, stepID, out := r.step()
		if out != nil {
			return out
		}
		if out := r.afterModel(resp, stepID); out != nil {
			return out
		}
	}
}

func (r *run) request() llm.ChatRequest {
	return llm.ChatRequest{
		System:     r.in.System,
		Messages:   append([]core.Message(nil), r.msgs...),
		Tools:      r.a.cfg.Tools.Tools(),
		MaxTokens:  r.a.cfg.MaxTokens,
		Effort:     r.a.cfg.Effort,
		Thinking:   r.a.cfg.Thinking,
		Extensions: r.a.cfg.Extensions,
	}
}

// step makes one model call and records it.
func (r *run) step() (*core.ChatResponse, string, *Outcome) {
	idx := len(r.steps)
	stepID := fmt.Sprintf("%s:%d", r.in.RunID, idx)
	started := r.a.now()
	r.a.emit(Event{Type: EventStepStart, RunID: r.in.RunID, Step: idx})

	ch, err := r.a.cfg.Model.Stream(r.ctx, r.request())
	var resp *core.ChatResponse
	if err == nil {
		resp, err = r.consume(ch, idx)
	}
	if err != nil {
		if r.cancelled() {
			return nil, "", r.cancelOutcome()
		}
		return nil, "", r.end(ReasonError, core.RunFailed, err.Error())
	}

	cost, perr := r.a.cfg.Model.Price(resp)
	if perr != nil {
		// refuse to run unmetered: an unpriced model would be free to the ledger
		return nil, "", r.end(ReasonError, core.RunFailed, perr.Error())
	}
	rec := core.StepRecord{ID: stepID, RunID: r.in.RunID, Index: idx, Message: resp.Message, StopReason: resp.StopReason,
		Usage: resp.Usage, Cost: cost, Model: resp.Model, StartedAt: started, EndedAt: r.a.now()}
	bg := context.WithoutCancel(r.ctx)

	// Order matters for a worker that dies part way. The step and its metering
	// come first: the provider has already billed the call, so it must be
	// recorded even if the message is lost. The message comes before any tool
	// runs: the tool calls in it are what a resume works from.
	if err := r.a.cfg.Steps.Save(bg, rec); err != nil {
		return nil, "", r.end(ReasonError, core.RunFailed, err.Error())
	}
	r.steps = append(r.steps, rec)
	r.usage = r.usage.Add(resp.Usage)
	r.cost += cost
	if r.a.cfg.Usage != nil {
		if err := r.a.cfg.Usage.Record(bg, core.UsageEvent{
			ID: "usage:" + stepID, Provider: resp.Provider, Model: resp.Model, Usage: resp.Usage, Cost: cost,
			WorkspaceID: r.in.WorkspaceID, RunID: r.in.RunID, StepID: stepID, At: r.a.now(),
		}); err != nil {
			return nil, "", r.end(ReasonError, core.RunFailed, "metering failed: "+err.Error())
		}
	}
	if err := r.append(resp.Message); err != nil {
		return nil, "", r.end(ReasonError, core.RunFailed, err.Error())
	}
	u := resp.Usage
	r.a.emit(Event{Type: EventStepEnd, RunID: r.in.RunID, Step: idx, Usage: &u, Cost: cost})
	return resp, stepID, nil
}

// consume drains the stream to its terminal event, forwarding events.
func (r *run) consume(ch <-chan llm.StreamEvent, idx int) (*core.ChatResponse, error) {
	for e := range ch {
		e := e
		r.a.emit(Event{Type: EventModel, RunID: r.in.RunID, Step: idx, Stream: &e})
		switch e.Type {
		case llm.EventMessageStop:
			if e.Response == nil {
				return nil, errors.New("agent: message_stop without a response")
			}
			return e.Response, nil
		case llm.EventError:
			return nil, e.Err
		}
	}
	if r.ctx.Err() != nil {
		return nil, r.ctx.Err()
	}
	return nil, errors.New("agent: the model stream ended without a terminal event")
}

// afterModel decides what the stop reason means. It returns nil to go round again.
func (r *run) afterModel(resp *core.ChatResponse, stepID string) *Outcome {
	uses := resp.Message.Content.ToolUses()
	switch resp.StopReason {
	case core.StopEndTurn, core.StopSequence:
		if len(uses) == 0 {
			m := resp.Message
			return &Outcome{Reason: ReasonCompleted, Status: core.RunSucceeded, Final: &m}
		}
		// the model stopped its turn but left tool calls: it means to use them
		return r.runTools(uses, resp)
	case core.StopToolUse:
		if len(uses) == 0 {
			return r.fail(ReasonError, "the model stopped for tool use but made no tool call")
		}
		return r.runTools(uses, resp)
	case core.StopMaxTokens:
		// checked before any tool runs: a cut-off turn never executes a call
		return r.failSettling(ReasonMaxTokens, "the response hit the output limit")
	case core.StopRefusal:
		return r.failSettling(ReasonRefused, "the model refused")
	case core.StopContextExceeded:
		return r.failSettling(ReasonContextExceeded, "the conversation no longer fits the model's context window")
	case core.StopCancelled:
		return r.cancelOutcome()
	default:
		return r.failSettling(ReasonError, "the model stopped with "+string(resp.StopReason))
	}
}

func (r *run) fail(reason Reason, detail string) *Outcome {
	return r.end(reason, core.RunFailed, detail)
}

// failSettling ends the run and answers any tool calls in the last message, so
// the stored history is valid for whoever continues it.
func (r *run) failSettling(reason Reason, detail string) *Outcome {
	if err := r.settlePending(CodeNotExecuted, detail+"; no tool was run"); err != nil {
		return r.fail(ReasonError, err.Error())
	}
	return r.fail(reason, detail)
}

// signature identifies a call by tool and canonical input, for loop detection.
func signature(c core.ToolUseBlock) string {
	var v any
	if err := json.Unmarshal(c.Input, &v); err != nil {
		return c.Name + "\x00" + string(c.Input)
	}
	canon, _ := json.Marshal(v) // map keys are sorted
	return c.Name + "\x00" + string(canon)
}

// runTools executes the calls of the last assistant message, in order, and
// appends their results as one message. It returns nil to continue the loop.
func (r *run) runTools(uses []core.ToolUseBlock, resp *core.ChatResponse) *Outcome {
	threshold := r.a.cfg.LoopThreshold
	if threshold <= 0 {
		threshold = 3
	}
	prior, _ := r.a.cfg.Calls.List(r.ctx, r.in.RunID)
	stepID := ""
	if n := len(r.steps); n > 0 {
		stepID = r.steps[n-1].ID
	}
	// Records are matched by step AND call id: ids are only unique within the
	// model's own turn, so a repeated id in a later step is a new call.
	recorded := map[string]core.ToolCallRecord{}
	for _, c := range prior {
		if c.StepID == stepID {
			recorded[c.ID] = c
		}
	}

	results := make(map[string]core.ToolResult, len(uses))
	finish := func(out *Outcome) *Outcome {
		// answer every call: the real results, then an error for any not run
		blocks := make(core.Content, 0, len(uses))
		why := "not executed"
		if out != nil {
			why = "the run ended before this tool ran: " + out.Detail
		}
		for _, tu := range uses {
			res, ok := results[tu.ID]
			if !ok {
				res = ErrorResult(tu.ID, CodeNotExecuted, why)
			}
			blocks = append(blocks, res.Block())
		}
		if err := r.append(core.Message{Role: core.RoleUser, Content: blocks}); err != nil {
			return r.fail(ReasonError, err.Error())
		}
		return out
	}

	for _, tu := range uses {
		if r.cancelled() {
			return finish(r.cancelOutcomeNoSettle())
		}
		call := core.ToolCall{ID: tu.ID, Name: tu.Name, Input: tu.Input}

		// loop detection counts the call being asked for, before it runs
		sig := signature(tu)
		if sig == r.lastSig {
			r.repeats++
		} else {
			r.lastSig, r.repeats = sig, 1
		}
		if r.repeats >= threshold {
			return finish(r.fail(ReasonLoop, fmt.Sprintf("%s was called with the same input %d times in a row", tu.Name, r.repeats)))
		}

		// already done before a pause or a crash: reuse the recorded result
		if rec, ok := recorded[tu.ID]; ok && rec.Completed {
			results[tu.ID] = core.ToolResult{ToolCallID: tu.ID, Content: rec.Output, IsError: rec.IsError}
			continue
		}
		// started but never finished: do not repeat what may have had effects
		if rec, ok := recorded[tu.ID]; ok && !rec.Completed && !rec.Paused && !r.a.cfg.Tools.ResumeSafe(tu.Name) {
			res := ErrorResult(tu.ID, CodeInterrupted, "this call was started but its outcome was lost when the worker stopped; check the current state before trying it again")
			results[tu.ID] = res
			if err := r.saveCall(stepID, call, res, 0, true); err != nil {
				return finish(r.fail(ReasonError, err.Error()))
			}
			continue
		}
		if msg, bad := invalidInput(resp, tu.ID); bad {
			res := ErrorResult(tu.ID, CodeInvalidJSON, msg)
			results[tu.ID] = res
			if err := r.saveCall(stepID, call, res, 0, true); err != nil {
				return finish(r.fail(ReasonError, err.Error()))
			}
			continue
		}

		// The "started" record must be durable BEFORE the tool runs. If it cannot
		// be written the tool does not run: a worker that dies after running it
		// would otherwise leave no trace, and a resume would run it twice.
		if err := r.saveCall(stepID, call, core.ToolResult{}, 0, false); err != nil {
			return finish(r.fail(ReasonError, "could not record the tool call, so it was not run: "+err.Error()))
		}
		r.a.emit(Event{Type: EventToolStart, RunID: r.in.RunID, Step: len(r.steps) - 1, Call: &call})
		t0 := r.a.now()
		res, err := r.a.cfg.Tools.Dispatch(r.ctx, call)
		var pe *PauseError
		switch {
		case errors.As(err, &pe):
			// The call did not happen. Mark the record paused so a resume dispatches
			// it again rather than treating it as interrupted mid-effect.
			if serr := r.saveCallState(stepID, call, core.ToolResult{}, 0, false, true); serr != nil {
				return r.fail(ReasonError, serr.Error())
			}
			o := r.end(ReasonPaused, pe.Status, pe.Detail)
			o.Ref = pe.Ref
			return o
		case err != nil:
			if r.cancelled() {
				return finish(r.cancelOutcomeNoSettle())
			}
			res = ErrorResult(tu.ID, CodeToolFailed, err.Error())
		}
		res.ToolCallID = tu.ID
		results[tu.ID] = res
		_ = r.saveCall(stepID, call, res, r.a.now().Sub(t0), true) // the result is in the results message either way
		r.a.emit(Event{Type: EventToolResult, RunID: r.in.RunID, Step: len(r.steps) - 1, Call: &call, Result: &res})
	}
	return finish(nil)
}

func (r *run) cancelOutcomeNoSettle() *Outcome {
	detail := "cancelled"
	if r.a.cfg.Cancel != nil && r.a.cfg.Cancel.Reason() != "" {
		detail = r.a.cfg.Cancel.Reason()
	}
	return r.end(ReasonCancelled, core.RunCancelled, detail)
}

func (r *run) saveCall(stepID string, call core.ToolCall, res core.ToolResult, d time.Duration, done bool) error {
	return r.saveCallState(stepID, call, res, d, done, false)
}

func (r *run) saveCallState(stepID string, call core.ToolCall, res core.ToolResult, d time.Duration, done, paused bool) error {
	rec := core.ToolCallRecord{Paused: paused, ID: call.ID, RunID: r.in.RunID, StepID: stepID, Name: call.Name, Input: call.Input,
		Completed: done, Output: res.Content, IsError: res.IsError, Duration: d, At: r.a.now()}
	return r.a.cfg.Calls.Save(context.WithoutCancel(r.ctx), rec)
}

// invalidInput reports whether the driver flagged this call's input as invalid
// (eager input streaming); resp is nil when the call is being resumed.
func invalidInput(resp *core.ChatResponse, id string) (string, bool) {
	if resp == nil {
		return "", false
	}
	msg, bad := resp.InvalidToolInputs[id]
	return msg, bad
}

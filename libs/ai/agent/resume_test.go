package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/thescaffold/gox-packages/libs/ai/agent"
	"github.com/thescaffold/gox-packages/libs/ai/core"
	aitesting "github.com/thescaffold/gox-packages/libs/ai/testing"
)

// A guarded tool pauses the run; once the gate opens, resuming runs it exactly once.
func TestPauseThenResumeRunsTheCallOnce(t *testing.T) {
	var gate atomic.Bool
	var ran atomic.Int64
	guarded := funcTool{name: "deploy", fn: func(context.Context, core.ToolCall) (core.Content, error) {
		if !gate.Load() {
			return nil, &agent.PauseError{Status: core.RunAwaitingApproval, Detail: "needs approval apr_1", Ref: "apr_1"}
		}
		ran.Add(1)
		return core.Text("deployed"), nil
	}}
	e := newEnv(t, []aitesting.Turn{call("d1", "deploy", map[string]any{"env": "prod"}), aitesting.Reply("all done")}, guarded)
	out := e.run(t, "ship it")
	if out.Reason != agent.ReasonPaused || out.Status != core.RunAwaitingApproval || out.Detail != "needs approval apr_1" || out.Ref != "apr_1" {
		t.Fatalf("%+v", out)
	}
	if e.status() != core.RunAwaitingApproval {
		t.Fatalf("status = %s", e.status())
	}
	if n := len(e.history(t)); n != 2 {
		t.Fatalf("a paused run must not write a results message yet; history has %d messages", n)
	}
	calls, _ := e.mem.ToolCalls().List(context.Background(), "run1")
	if len(calls) != 1 || calls[0].Completed {
		t.Fatalf("the paused call must stay open: %+v", calls)
	}
	modelCalls := e.fake.CallCount()

	// still closed: resuming pauses again, and does not call the model
	if out, err := e.resume(t); err != nil || out.Reason != agent.ReasonPaused || e.fake.CallCount() != modelCalls {
		t.Fatalf("%+v %v calls=%d", out, err, e.fake.CallCount())
	}

	gate.Store(true)
	out, err := e.resume(t)
	if err != nil || out.Reason != agent.ReasonCompleted || out.Text() != "all done" {
		t.Fatalf("%+v %v", out, err)
	}
	if ran.Load() != 1 {
		t.Fatalf("the guarded tool ran %d times", ran.Load())
	}
	validHistory(t, e.history(t), true)
	if e.status() != core.RunSucceeded {
		t.Fatalf("status = %s", e.status())
	}
}

func TestBlockedCreditsPauseLeavesTheRunResumable(t *testing.T) {
	var funded atomic.Bool
	tool := funcTool{name: "t", fn: func(context.Context, core.ToolCall) (core.Content, error) {
		if !funded.Load() {
			return nil, &agent.PauseError{Status: core.RunBlockedCredits, Detail: "top up"}
		}
		return core.Text("ok"), nil
	}}
	e := newEnv(t, []aitesting.Turn{call("a", "t", map[string]any{}), aitesting.Reply("fin")}, tool)
	if out := e.run(t, "go"); out.Status != core.RunBlockedCredits {
		t.Fatalf("%+v", out)
	}
	funded.Store(true)
	if out, err := e.resume(t); err != nil || out.Reason != agent.ReasonCompleted {
		t.Fatalf("%+v %v", out, err)
	}
}

// Calls completed before the pause are not run again when a later call in the
// same message was the one that paused.
func TestPauseInTheMiddleOfAMessageKeepsEarlierResults(t *testing.T) {
	var gate atomic.Bool
	var first, second atomic.Int64
	tool := funcTool{name: "t", fn: func(_ context.Context, c core.ToolCall) (core.Content, error) {
		if strings.Contains(string(c.Input), `"n":1`) {
			first.Add(1)
			return core.Text("one"), nil
		}
		if !gate.Load() {
			return nil, &agent.PauseError{Status: core.RunAwaitingApproval, Detail: "x"}
		}
		second.Add(1)
		return core.Text("two"), nil
	}}
	reply := &core.ChatResponse{StopReason: core.StopToolUse, Usage: core.Usage{InputTokens: 1, OutputTokens: 1}, Message: core.Message{Role: core.RoleAssistant, Content: core.Content{
		core.ToolUseBlock{ID: "a", Name: "t", Input: json.RawMessage(`{"n":1}`)}, core.ToolUseBlock{ID: "b", Name: "t", Input: json.RawMessage(`{"n":2}`)}}}}
	e := newEnv(t, []aitesting.Turn{{Reply: reply}, aitesting.Reply("end")}, tool)
	if out := e.run(t, "go"); out.Reason != agent.ReasonPaused {
		t.Fatalf("%+v", out)
	}
	gate.Store(true)
	if out, err := e.resume(t); err != nil || out.Reason != agent.ReasonCompleted {
		t.Fatalf("%+v %v", out, err)
	}
	if first.Load() != 1 || second.Load() != 1 {
		t.Fatalf("first ran %d, second ran %d; each must run exactly once", first.Load(), second.Load())
	}
	res := e.history(t)[2]
	if len(res.Content) != 2 || res.Content[0].(core.ToolResultBlock).Content.PlainText() != "one" || res.Content[1].(core.ToolResultBlock).Content.PlainText() != "two" {
		t.Fatalf("%s", dump(e.history(t)))
	}
}

// Seeds a run as a dead worker would have left it: the assistant message with
// two calls is stored, plus whatever call records reached the store.
func seedCrashed(t *testing.T, e *env, recs ...core.ToolCallRecord) {
	t.Helper()
	ctx := context.Background()
	_ = e.mem.Append(ctx, "run1", core.UserText("go"))
	_ = e.mem.Append(ctx, "run1", core.Message{Role: core.RoleAssistant, Content: core.Content{
		core.ToolUseBlock{ID: "done", Name: "w", Input: json.RawMessage(`{"n":1}`)},
		core.ToolUseBlock{ID: "half", Name: "w", Input: json.RawMessage(`{"n":2}`)},
		core.ToolUseBlock{ID: "never", Name: "w", Input: json.RawMessage(`{"n":3}`)},
	}})
	_ = e.mem.Steps().Save(ctx, core.StepRecord{ID: "run1:0", RunID: "run1", Index: 0, Usage: core.Usage{InputTokens: 1, OutputTokens: 1}})
	for _, r := range recs {
		r.RunID, r.StepID = "run1", "run1:0"
		_ = e.mem.ToolCalls().Save(ctx, r)
	}
}

func TestResumeAfterWorkerDeathReusesCompletedSkipsUnsafeAndRunsTheRest(t *testing.T) {
	var ran []string
	tool := funcTool{name: "w", fn: func(_ context.Context, c core.ToolCall) (core.Content, error) {
		ran = append(ran, c.ID)
		return core.Text("fresh " + c.ID), nil
	}}
	e := newEnv(t, []aitesting.Turn{aitesting.Reply("finished")}, tool)
	seedCrashed(t, e,
		core.ToolCallRecord{ID: "done", Name: "w", Completed: true, Output: core.Text("from before the crash")},
		core.ToolCallRecord{ID: "half", Name: "w", Completed: false}, // started, result lost
	)
	out, err := e.resume(t)
	if err != nil || out.Reason != agent.ReasonCompleted || out.Text() != "finished" {
		t.Fatalf("%+v %v", out, err)
	}
	if strings.Join(ran, ",") != "never" {
		t.Fatalf("ran %v; the finished call must be reused, the interrupted one not blindly repeated, and only the never-started one run", ran)
	}
	res := e.history(t)[2].Content
	got := func(i int) core.ToolResultBlock { return res[i].(core.ToolResultBlock) }
	if got(0).Content.PlainText() != "from before the crash" || got(0).IsError {
		t.Fatalf("%s", dump(e.history(t)))
	}
	if !got(1).IsError || !strings.HasPrefix(got(1).Content.PlainText(), agent.CodeInterrupted) {
		t.Fatalf("%s", dump(e.history(t)))
	}
	if got(2).Content.PlainText() != "fresh never" {
		t.Fatalf("%s", dump(e.history(t)))
	}
	validHistory(t, e.history(t), true)
}

func TestInterruptedResumeSafeToolIsRerun(t *testing.T) {
	var ran []string
	tool := funcTool{name: "w", resumeSafe: true, fn: func(_ context.Context, c core.ToolCall) (core.Content, error) {
		ran = append(ran, c.ID)
		return core.Text("again " + c.ID), nil
	}}
	e := newEnv(t, []aitesting.Turn{aitesting.Reply("ok")}, tool)
	seedCrashed(t, e, core.ToolCallRecord{ID: "half", Name: "w", Completed: false})
	if out, err := e.resume(t); err != nil || out.Reason != agent.ReasonCompleted {
		t.Fatalf("%+v %v", out, err)
	}
	if strings.Join(ran, ",") != "done,half,never" {
		t.Fatalf("ran %v", ran)
	}
}

func TestResumingAFinishedRunDoesNotCallTheModel(t *testing.T) {
	e := newEnv(t, []aitesting.Turn{aitesting.Reply("the end")})
	e.run(t, "go")
	calls := e.fake.CallCount()
	out, err := e.resume(t)
	if err != nil || out.Reason != agent.ReasonCompleted || out.Text() != "the end" || e.fake.CallCount() != calls {
		t.Fatalf("%+v %v calls=%d", out, err, e.fake.CallCount())
	}
	if out.Steps != 1 {
		t.Fatalf("totals must cover the whole run: %+v", out)
	}
}

// ---- crash injection -------------------------------------------------------

var errCrash = errors.New("worker died")

// crasher makes the Nth write to the stores fail, as a worker dying at that
// point would: the write does not happen and nothing later does either.
type crasher struct {
	at    int
	count int
}

func (c *crasher) tick() error {
	c.count++
	if c.at > 0 && c.count >= c.at {
		return errCrash
	}
	return nil
}

type crashMsgs struct {
	core.MessageStore
	c *crasher
}

func (s crashMsgs) Append(ctx context.Context, id string, m core.Message) error {
	if err := s.c.tick(); err != nil {
		return err
	}
	return s.MessageStore.Append(ctx, id, m)
}

type crashSteps struct {
	core.AgentStepStore
	c *crasher
}

func (s crashSteps) Save(ctx context.Context, r core.StepRecord) error {
	if err := s.c.tick(); err != nil {
		return err
	}
	return s.AgentStepStore.Save(ctx, r)
}

type crashCalls struct {
	core.AgentToolCallStore
	c *crasher
}

func (s crashCalls) Save(ctx context.Context, r core.ToolCallRecord) error {
	if err := s.c.tick(); err != nil {
		return err
	}
	return s.AgentToolCallStore.Save(ctx, r)
}

// For every point at which a worker can die, resuming on another worker
// finishes the run, every tool call takes effect exactly once, and the history
// stays valid. This is the property the lease-and-resume design (M1-31) needs.
func TestKillingTheWorkerAtAnyWriteResumesWithEachToolRunExactlyOnce(t *testing.T) {
	for crashAt := 1; crashAt <= 14; crashAt++ {
		t.Run(fmt.Sprintf("write %d", crashAt), func(t *testing.T) {
			effects := map[string]int{}
			tool := funcTool{name: "w", fn: func(_ context.Context, c core.ToolCall) (core.Content, error) {
				effects[string(c.Input)]++
				return core.Text("did " + string(c.Input)), nil
			}}
			turns := []aitesting.Turn{
				call("a1", "w", map[string]any{"n": 1}),
				call("a2", "w", map[string]any{"n": 2}),
				aitesting.Reply("finished"),
			}
			e := newEnv(t, turns, tool)
			mem := e.mem

			// worker 1 dies at the crashAt-th write
			cr := &crasher{at: crashAt}
			e.cfg.Messages, e.cfg.Steps, e.cfg.Calls = crashMsgs{mem, cr}, crashSteps{mem.Steps(), cr}, crashCalls{mem.ToolCalls(), cr}
			first := core.UserText("go")
			out1, err := e.agent(t).Run(context.Background(), agent.Input{RunID: "run1", Message: &first})
			if cr.count >= crashAt && err == nil && out1.Reason != agent.ReasonError {
				t.Fatalf("worker should have died: %+v", out1)
			}

			// worker 2 picks the run up from the store alone. The model's script
			// continues from however many replies were actually stored.
			assistant := 0
			for _, m := range e.history(t) {
				if m.Role == core.RoleAssistant {
					assistant++
				}
			}
			e.cfg.Messages, e.cfg.Steps, e.cfg.Calls = mem, mem.Steps(), mem.ToolCalls()
			rest := turns[assistant:]
			if len(rest) == 0 {
				rest = turns[len(turns)-1:]
			}
			e.fake = aitesting.NewFake(rest...)
			e.cfg.Model = agent.DriverModel{Driver: e.fake, ModelID: aitesting.FakeModel, Pricing: pricing}

			var out2 *agent.Outcome
			if len(e.history(t)) == 0 {
				// died before anything was stored: the caller simply starts the run again
				m := core.UserText("go")
				out2, err = e.agent(t).Run(context.Background(), agent.Input{RunID: "run1", Message: &m})
			} else {
				out2, err = e.resume(t)
			}
			if err != nil || out2.Reason != agent.ReasonCompleted {
				t.Fatalf("resume: %+v %v\n%s", out2, err, dump(e.history(t)))
			}
			for input, n := range effects {
				if n != 1 {
					t.Fatalf("tool input %s took effect %d times", input, n)
				}
			}
			if len(effects) != 2 {
				t.Fatalf("effects = %v, want both calls", effects)
			}
			validHistory(t, e.history(t), true)
		})
	}
}

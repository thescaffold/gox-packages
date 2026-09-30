package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

var full = llm.ModelInfo{ID: "m", Provider: "p", MaxOutputTokens: 1000, SupportsTools: true, SupportsForcedToolChoice: true}

func base() llm.ChatRequest {
	return llm.ChatRequest{Model: "m", Messages: []core.Message{core.UserText("hi")}}
}

var obj = map[string]any{"type": "object"}

func TestValidateAcceptsAWellFormedRequest(t *testing.T) {
	r := base()
	r.System = []llm.SystemBlock{{Text: "be brief", Cache: &core.CacheHint{}}}
	r.Tools = []core.ToolDef{{Name: "a", InputSchema: obj}}
	r.ToolChoice = llm.ToolChoice{Mode: llm.ToolOne, Name: "a"}
	r.Effort, r.Thinking, r.MaxTokens = llm.EffortHigh, llm.ThinkingOff, 1000
	r.Budget = &llm.TaskBudget{TotalTokens: 5}
	if err := llm.ValidateRequest(full, r); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	use := core.Message{Role: core.RoleAssistant, Content: core.Content{core.ToolUseBlock{ID: "t1", Name: "a", Input: json.RawMessage(`{}`)}}}
	res := func(id string) core.Message {
		return core.Message{Role: core.RoleUser, Content: core.Content{core.ToolResultBlock{ToolUseID: id, Content: core.Text("r")}}}
	}
	twice := core.Message{Role: core.RoleUser, Content: core.Content{
		core.ToolResultBlock{ToolUseID: "t1", Content: core.Text("a")}, core.ToolResultBlock{ToolUseID: "t1", Content: core.Text("b")}}}
	tool := core.ToolDef{Name: "a", InputSchema: obj}

	cases := map[string]func(*llm.ChatRequest){
		"no model":          func(r *llm.ChatRequest) { r.Model = "" },
		"model mismatch":    func(r *llm.ChatRequest) { r.Model = "other" },
		"no messages":       func(r *llm.ChatRequest) { r.Messages = nil },
		"bad effort":        func(r *llm.ChatRequest) { r.Effort = "x" },
		"bad thinking":      func(r *llm.ChatRequest) { r.Thinking = "x" },
		"negative max":      func(r *llm.ChatRequest) { r.MaxTokens = -1 },
		"max above model":   func(r *llm.ChatRequest) { r.MaxTokens = 1001 },
		"zero budget":       func(r *llm.ChatRequest) { r.Budget = &llm.TaskBudget{} },
		"empty system":      func(r *llm.ChatRequest) { r.System = []llm.SystemBlock{{}} },
		"nameless tool":     func(r *llm.ChatRequest) { r.Tools = []core.ToolDef{{InputSchema: obj}} },
		"schemaless tool":   func(r *llm.ChatRequest) { r.Tools = []core.ToolDef{{Name: "a"}} },
		"duplicate tool":    func(r *llm.ChatRequest) { r.Tools = []core.ToolDef{tool, tool} },
		"unknown mode":      func(r *llm.ChatRequest) { r.ToolChoice.Mode = "x" },
		"any without tools": func(r *llm.ChatRequest) { r.ToolChoice.Mode = llm.ToolAny },
		"named not offered": func(r *llm.ChatRequest) {
			r.Tools = []core.ToolDef{tool}
			r.ToolChoice = llm.ToolChoice{Mode: llm.ToolOne, Name: "z"}
		},
		"system role": func(r *llm.ChatRequest) {
			r.Messages = []core.Message{{Role: core.RoleSystem, Content: core.Text("x")}, core.UserText("y")}
		},
		"invalid message":     func(r *llm.ChatRequest) { r.Messages = []core.Message{{Role: core.RoleUser}} },
		"ends on assistant":   func(r *llm.ChatRequest) { r.Messages = []core.Message{core.UserText("a"), core.AssistantText("b")} },
		"orphan result":       func(r *llm.ChatRequest) { r.Messages = []core.Message{res("zzz")} },
		"unanswered call":     func(r *llm.ChatRequest) { r.Messages = []core.Message{core.UserText("a"), use, core.UserText("b")} },
		"answered twice":      func(r *llm.ChatRequest) { r.Messages = []core.Message{core.UserText("a"), use, twice} },
		"result answers late": func(r *llm.ChatRequest) { r.Messages = []core.Message{core.UserText("a"), use, res("t1"), res("t1")} },
		"duplicate call id": func(r *llm.ChatRequest) {
			r.Messages = []core.Message{core.UserText("a"), {Role: core.RoleAssistant, Content: core.Content{
				core.ToolUseBlock{ID: "d", Name: "a", Input: json.RawMessage(`{}`)}, core.ToolUseBlock{ID: "d", Name: "a", Input: json.RawMessage(`{}`)}}}}
		},
	}
	for name, mut := range cases {
		r := base()
		mut(&r)
		err := llm.ValidateRequest(full, r)
		if !errors.Is(err, llm.ErrInvalidRequest) {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	// a valid tool round trip is accepted
	r := base()
	r.Tools = []core.ToolDef{tool}
	r.Messages = []core.Message{core.UserText("a"), use, res("t1")}
	if err := llm.ValidateRequest(full, r); err != nil {
		t.Errorf("valid tool round trip rejected: %v", err)
	}
}

func TestValidateForcedToolChoiceFollowsCapability(t *testing.T) {
	noForce := full
	noForce.SupportsForcedToolChoice = false
	r := base()
	r.Tools = []core.ToolDef{{Name: "a", InputSchema: obj}}
	for _, c := range []llm.ToolChoice{{Mode: llm.ToolAny}, {Mode: llm.ToolOne, Name: "a"}} {
		r.ToolChoice = c
		if err := llm.ValidateRequest(noForce, r); !errors.Is(err, llm.ErrInvalidRequest) {
			t.Errorf("%s accepted on a model without support: %v", c.Mode, err)
		}
		if err := llm.ValidateRequest(full, r); err != nil {
			t.Errorf("%s rejected on a model with support: %v", c.Mode, err)
		}
	}
	for _, c := range []llm.ToolChoice{{}, {Mode: llm.ToolAuto}, {Mode: llm.ToolNone}} {
		r.ToolChoice = c
		if err := llm.ValidateRequest(noForce, r); err != nil {
			t.Errorf("%q rejected: %v", c.Mode, err)
		}
	}
	noTools := full
	noTools.SupportsTools = false
	if err := llm.ValidateRequest(noTools, r); !errors.Is(err, llm.ErrInvalidRequest) {
		t.Errorf("tools accepted on a model without tool support: %v", err)
	}
}

func ev(t llm.EventType, f func(*llm.StreamEvent)) llm.StreamEvent {
	e := llm.StreamEvent{Type: t}
	if f != nil {
		f(&e)
	}
	return e
}

func feed(t *testing.T, a *llm.Accumulator, evs ...llm.StreamEvent) {
	t.Helper()
	for _, e := range evs {
		if err := a.Add(e); err != nil {
			t.Fatalf("Add(%s): %v", e.Type, err)
		}
	}
}

func TestAccumulatorBuildsEveryBlockInOrder(t *testing.T) {
	var a llm.Accumulator
	a.SetProviderID("msg_1")
	u := core.Usage{InputTokens: 4, OutputTokens: 9}
	feed(t, &a,
		ev(llm.EventMessageStart, func(e *llm.StreamEvent) { e.Model = "m" }),
		ev(llm.EventBlockStart, func(e *llm.StreamEvent) { e.Index = 0; e.BlockType = core.TypeThinking }),
		ev(llm.EventThinkingDelta, func(e *llm.StreamEvent) { e.Index = 0; e.Text = "hm" }),
		ev(llm.EventThinkingDelta, func(e *llm.StreamEvent) { e.Index = 0; e.Text = "m" }),
		ev(llm.EventSignature, func(e *llm.StreamEvent) { e.Index = 0; e.Text = "si" }),
		ev(llm.EventSignature, func(e *llm.StreamEvent) { e.Index = 0; e.Text = "g" }),
		ev(llm.EventBlockStop, func(e *llm.StreamEvent) { e.Index = 0 }),
		ev(llm.EventBlockStart, func(e *llm.StreamEvent) { e.Index = 1; e.BlockType = core.TypeRedactedThinking; e.Data = "red" }),
		ev(llm.EventBlockStart, func(e *llm.StreamEvent) { e.Index = 2; e.BlockType = core.TypeText }),
		ev(llm.EventTextDelta, func(e *llm.StreamEvent) { e.Index = 2; e.Text = "hel" }),
		ev(llm.EventTextDelta, func(e *llm.StreamEvent) { e.Index = 2; e.Text = "lo" }),
		ev(llm.EventBlockStart, func(e *llm.StreamEvent) {
			e.Index = 3
			e.BlockType = core.TypeToolUse
			e.ToolID = "t"
			e.ToolName = "fs"
		}),
		ev(llm.EventToolInput, func(e *llm.StreamEvent) { e.Index = 3; e.PartialJSON = `{"a":` }),
		ev(llm.EventToolInput, func(e *llm.StreamEvent) { e.Index = 3; e.PartialJSON = `1}` }),
		ev(llm.EventMessageDelta, func(e *llm.StreamEvent) { e.StopReason = core.StopToolUse; e.Usage = &u }),
	)
	r, err := a.Response()
	if err != nil {
		t.Fatal(err)
	}
	want := core.Content{
		core.ThinkingBlock{Thinking: "hmm", Signature: "sig"},
		core.RedactedThinkingBlock{Data: "red"},
		core.TextBlock{Text: "hello"},
		core.ToolUseBlock{ID: "t", Name: "fs", Input: json.RawMessage(`{"a":1}`)},
	}
	got, _ := json.Marshal(r.Message.Content)
	w, _ := json.Marshal(want)
	if string(got) != string(w) {
		t.Fatalf("content:\n got %s\nwant %s", got, w)
	}
	if r.Message.Role != core.RoleAssistant || r.StopReason != core.StopToolUse || r.Usage != u || r.Model != "m" || r.ProviderID != "msg_1" {
		t.Fatalf("envelope: %+v", r)
	}
}

func TestAccumulatorEmptyToolInputBecomesEmptyObject(t *testing.T) {
	var a llm.Accumulator
	feed(t, &a,
		ev(llm.EventBlockStart, func(e *llm.StreamEvent) { e.BlockType = core.TypeToolUse; e.ToolID = "t"; e.ToolName = "x" }),
		ev(llm.EventMessageDelta, func(e *llm.StreamEvent) { e.StopReason = core.StopToolUse }),
	)
	r, err := a.Response()
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Message.Content.ToolUses()[0].Input) != "{}" {
		t.Fatalf("input = %s", r.Message.Content.ToolUses()[0].Input)
	}
}

func TestAccumulatorRefusesUnparseableToolInput(t *testing.T) {
	var a llm.Accumulator
	feed(t, &a,
		ev(llm.EventBlockStart, func(e *llm.StreamEvent) { e.BlockType = core.TypeToolUse; e.ToolID = "t"; e.ToolName = "x" }),
		ev(llm.EventToolInput, func(e *llm.StreamEvent) { e.PartialJSON = `{"a":` }), // truncated
		ev(llm.EventMessageDelta, func(e *llm.StreamEvent) { e.StopReason = core.StopMaxTokens }),
	)
	if _, err := a.Response(); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v", err)
	}
}

func TestAccumulatorNeedsAStopReason(t *testing.T) {
	var a llm.Accumulator
	feed(t, &a, ev(llm.EventBlockStart, func(e *llm.StreamEvent) { e.BlockType = core.TypeText }))
	if _, err := a.Response(); err == nil {
		t.Fatal("response built without a stop reason")
	}
}

func TestAccumulatorRejectsMalformedSequences(t *testing.T) {
	start := func(i int, typ string) llm.StreamEvent {
		return ev(llm.EventBlockStart, func(e *llm.StreamEvent) { e.Index = i; e.BlockType = typ })
	}
	cases := map[string][]llm.StreamEvent{
		"delta before start": {ev(llm.EventTextDelta, nil)},
		"stop before start":  {ev(llm.EventBlockStop, nil)},
		"start twice":        {start(0, core.TypeText), start(0, core.TypeText)},
		"unsupported type":   {start(0, "image")},
		"text on tool":       {start(0, core.TypeToolUse), ev(llm.EventTextDelta, nil)},
		"thinking on text":   {start(0, core.TypeText), ev(llm.EventThinkingDelta, nil)},
		"signature on text":  {start(0, core.TypeText), ev(llm.EventSignature, nil)},
		"tool input on text": {start(0, core.TypeText), ev(llm.EventToolInput, nil)},
		"terminal not input": {ev(llm.EventMessageStop, nil)},
		"error not input":    {ev(llm.EventError, nil)},
	}
	for name, evs := range cases {
		var a llm.Accumulator
		var err error
		for _, e := range evs {
			if err = a.Add(e); err != nil {
				break
			}
		}
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCollect(t *testing.T) {
	resp := &core.ChatResponse{StopReason: core.StopEndTurn}
	send := func(evs ...llm.StreamEvent) <-chan llm.StreamEvent {
		ch := make(chan llm.StreamEvent, len(evs))
		for _, e := range evs {
			ch <- e
		}
		close(ch)
		return ch
	}
	got, err := llm.Collect(context.Background(), send(ev(llm.EventTextDelta, nil), llm.StreamEvent{Type: llm.EventMessageStop, Response: resp}))
	if err != nil || got != resp {
		t.Fatalf("got %v, %v", got, err)
	}
	boom := errors.New("boom")
	if _, err := llm.Collect(context.Background(), send(llm.StreamEvent{Type: llm.EventError, Err: boom})); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	for name, ch := range map[string]<-chan llm.StreamEvent{
		"closed early":     send(ev(llm.EventTextDelta, nil)),
		"stop no response": send(ev(llm.EventMessageStop, nil)),
		"error no error":   send(ev(llm.EventError, nil)),
	} {
		if _, err := llm.Collect(context.Background(), ch); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := llm.Collect(ctx, make(chan llm.StreamEvent)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestTerminalAndEffort(t *testing.T) {
	if !(llm.StreamEvent{Type: llm.EventMessageStop}).Terminal() || !(llm.StreamEvent{Type: llm.EventError}).Terminal() || (llm.StreamEvent{Type: llm.EventTextDelta}).Terminal() {
		t.Fatal("Terminal wrong")
	}
	for _, e := range []llm.Effort{"", llm.EffortLow, llm.EffortMedium, llm.EffortHigh, llm.EffortXHigh, llm.EffortMax} {
		if !e.Valid() {
			t.Errorf("%q invalid", e)
		}
	}
	if llm.Effort("max!").Valid() {
		t.Fatal("bogus effort valid")
	}
}

func TestValidateRequiresAModelEvenWithoutCapabilities(t *testing.T) {
	r := base()
	r.Model = ""
	if err := llm.ValidateRequest(llm.ModelInfo{}, r); !errors.Is(err, llm.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
	r.Model = "m"
	if err := llm.ValidateRequest(llm.ModelInfo{}, r); err != nil {
		t.Fatalf("a request naming a model with unknown capabilities must pass: %v", err)
	}
}

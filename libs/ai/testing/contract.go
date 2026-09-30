package aitesting

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

// Scenario is what the model does on the next call, stated provider-neutrally.
// A Harness turns it into whatever its driver needs: the fake plays it
// directly; the Anthropic driver replays a recorded HTTP fixture whose content
// is the same.
type Scenario struct {
	// Reply is what the model says. Ignored when Err or Stall is set.
	Reply *core.ChatResponse
	// Err is a failure the provider returns.
	Err *core.ProviderError
	// Stall: the provider accepts the call and never answers, until cancelled.
	Stall bool
}

// Harness adapts one driver to the contract.
type Harness interface {
	// Model is a model id the driver serves; requests in the suite use it.
	Model() string
	// Driver returns a driver primed so its next call plays sc.
	Driver(t *testing.T, sc Scenario) llm.ProviderDriver
	// ForcedToolChoice is whether Model() supports ToolAny / ToolOne; the
	// suite checks the driver accepts or rejects accordingly.
	ForcedToolChoice() bool
}

// RunDriverContract is the suite every ProviderDriver must pass. It asserts
// behaviour a caller relies on, not how the driver gets there.
func RunDriverContract(t *testing.T, h Harness) {
	t.Helper()
	c := contract{h: h}
	for _, tc := range []struct {
		name string
		fn   func(*testing.T)
	}{
		{"ModelsReportsTheModel", c.modelsReportsTheModel},
		{"ChatText", c.chatText},
		{"ChatToolUse", c.chatToolUse},
		{"ChatThinkingIsPreserved", c.chatThinking},
		{"RefusalIsAnOutcomeNotAnError", c.refusal},
		{"MaxTokensIsAnOutcome", c.maxTokens},
		{"StreamAssemblesTheSameAsChat", c.streamEqualsChat},
		{"StreamHasExactlyOneTerminalEventThenCloses", c.streamTerminal},
		{"StreamDeltasConcatenateToTheFinalText", c.streamDeltas},
		{"StreamCancelEndsWithAnErrorAndCloses", c.streamCancel},
		{"StreamCancelMidwayStillEndsWithATerminalEvent", c.streamCancelMidway},
		{"ChatCancelReturnsPromptly", c.chatCancel},
		{"ProviderErrorsAreTyped", c.typedErrors},
		{"StreamProviderErrorsAreTyped", c.typedStreamErrors},
		{"RejectsInvalidRequestsBeforeAnyCall", c.rejectsInvalid},
		{"ForcedToolChoiceMatchesCapability", c.forcedToolChoice},
		{"DoesNotMutateTheRequest", c.doesNotMutate},
		{"CountTokensIsPositiveOrUnsupported", c.countTokens},
	} {
		t.Run(tc.name, tc.fn)
	}
}

type contract struct{ h Harness }

func (c contract) req(msgs ...core.Message) llm.ChatRequest {
	if len(msgs) == 0 {
		msgs = []core.Message{core.UserText("hello")}
	}
	return llm.ChatRequest{Model: c.h.Model(), Messages: msgs, MaxTokens: 256}
}

func textReply(s string) *core.ChatResponse {
	return &core.ChatResponse{
		Message:    core.AssistantText(s),
		StopReason: core.StopEndTurn,
		Usage:      core.Usage{InputTokens: 12, OutputTokens: 7, CacheReadTokens: 3},
	}
}

func (c contract) driver(t *testing.T, sc Scenario) llm.ProviderDriver {
	t.Helper()
	d := c.h.Driver(t, sc)
	if d == nil {
		t.Fatal("harness returned no driver")
	}
	return d
}

func (c contract) chat(t *testing.T, sc Scenario) *core.ChatResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := c.driver(t, sc).Chat(ctx, c.req())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	return r
}

func (c contract) modelsReportsTheModel(t *testing.T) {
	d := c.driver(t, Scenario{Reply: textReply("x")})
	if d.Provider() == "" {
		t.Error("Provider() is empty")
	}
	ms, err := d.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.ID == c.h.Model() {
			if m.Provider != d.Provider() {
				t.Errorf("model reports provider %q, driver %q", m.Provider, d.Provider())
			}
			if m.SupportsForcedToolChoice != c.h.ForcedToolChoice() {
				t.Errorf("model reports forced tool choice = %v, harness says %v", m.SupportsForcedToolChoice, c.h.ForcedToolChoice())
			}
			return
		}
	}
	t.Fatalf("Models() does not list %q: %+v", c.h.Model(), ms)
}

func (c contract) chatText(t *testing.T) {
	r := c.chat(t, Scenario{Reply: textReply("hello there")})
	if r.Message.Role != core.RoleAssistant {
		t.Errorf("role = %q", r.Message.Role)
	}
	if got := r.Message.Content.PlainText(); got != "hello there" {
		t.Errorf("text = %q", got)
	}
	if r.StopReason != core.StopEndTurn {
		t.Errorf("stop = %q", r.StopReason)
	}
	if r.Usage != (core.Usage{InputTokens: 12, OutputTokens: 7, CacheReadTokens: 3}) {
		t.Errorf("usage = %+v", r.Usage)
	}
	if r.Model == "" {
		t.Error("response does not say which model answered")
	}
}

func toolReply() *core.ChatResponse {
	return &core.ChatResponse{
		Message: core.Message{Role: core.RoleAssistant, Content: core.Content{
			core.TextBlock{Text: "let me look"},
			core.ToolUseBlock{ID: "toolu_1", Name: "fs_read", Input: json.RawMessage(`{"path":"a/b.txt","lines":[1,2]}`)},
		}},
		StopReason: core.StopToolUse,
		Usage:      core.Usage{InputTokens: 20, OutputTokens: 15},
	}
}

func (c contract) chatToolUse(t *testing.T) {
	r := c.chat(t, Scenario{Reply: toolReply()})
	if r.StopReason != core.StopToolUse {
		t.Fatalf("stop = %q", r.StopReason)
	}
	uses := r.Message.Content.ToolUses()
	if len(uses) != 1 || uses[0].ID != "toolu_1" || uses[0].Name != "fs_read" {
		t.Fatalf("tool uses = %+v", uses)
	}
	var in, want map[string]any
	if err := json.Unmarshal(uses[0].Input, &in); err != nil {
		t.Fatalf("tool input is not JSON: %v", err)
	}
	_ = json.Unmarshal([]byte(`{"path":"a/b.txt","lines":[1,2]}`), &want)
	if !reflect.DeepEqual(in, want) {
		t.Errorf("tool input = %v", in)
	}
}

func thinkingReply() *core.ChatResponse {
	return &core.ChatResponse{
		Message: core.Message{Role: core.RoleAssistant, Content: core.Content{
			core.ThinkingBlock{Thinking: "weighing it up", Signature: "c2lnbmF0dXJl"},
			core.TextBlock{Text: "done"},
		}},
		StopReason: core.StopEndTurn,
		Usage:      core.Usage{InputTokens: 9, OutputTokens: 30},
	}
}

func (c contract) chatThinking(t *testing.T) {
	r := c.chat(t, Scenario{Reply: thinkingReply()})
	var th *core.ThinkingBlock
	for _, b := range r.Message.Content {
		if x, ok := b.(core.ThinkingBlock); ok {
			th = &x
		}
	}
	if th == nil {
		t.Fatalf("thinking block dropped: %+v", r.Message.Content)
	}
	// history is append-only: the block must come back exactly as produced
	if th.Thinking != "weighing it up" || th.Signature != "c2lnbmF0dXJl" {
		t.Errorf("thinking altered: %+v", *th)
	}
	if r.Message.Content.PlainText() != "done" {
		t.Errorf("text = %q", r.Message.Content.PlainText())
	}
}

func (c contract) refusal(t *testing.T) {
	r := c.chat(t, Scenario{Reply: &core.ChatResponse{
		Message:    core.AssistantText("I can't help with that."),
		StopReason: core.StopRefusal,
		Usage:      core.Usage{InputTokens: 5, OutputTokens: 6},
	}})
	if r.StopReason != core.StopRefusal {
		t.Fatalf("stop = %q, want refusal", r.StopReason)
	}
}

func (c contract) maxTokens(t *testing.T) {
	r := c.chat(t, Scenario{Reply: &core.ChatResponse{
		Message:    core.AssistantText("cut of"),
		StopReason: core.StopMaxTokens,
		Usage:      core.Usage{InputTokens: 5, OutputTokens: 256},
	}})
	if r.StopReason != core.StopMaxTokens {
		t.Fatalf("stop = %q, want max_tokens", r.StopReason)
	}
}

func (c contract) stream(t *testing.T, sc Scenario) []llm.StreamEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := c.driver(t, sc).Stream(ctx, c.req())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var evs []llm.StreamEvent
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return evs
			}
			evs = append(evs, e)
		case <-ctx.Done():
			t.Fatalf("stream did not close; got %d events", len(evs))
		}
	}
}

func (c contract) streamEqualsChat(t *testing.T) {
	for name, reply := range map[string]*core.ChatResponse{
		"text": textReply("hello there"), "tool": toolReply(), "thinking": thinkingReply(),
	} {
		t.Run(name, func(t *testing.T) {
			want := c.chat(t, Scenario{Reply: reply})
			evs := c.stream(t, Scenario{Reply: reply})
			last := evs[len(evs)-1]
			if last.Type != llm.EventMessageStop || last.Response == nil {
				t.Fatalf("last event = %+v", last)
			}
			got := last.Response
			if got.StopReason != want.StopReason || got.Usage != want.Usage {
				t.Errorf("stream stop/usage = %q %+v; chat = %q %+v", got.StopReason, got.Usage, want.StopReason, want.Usage)
			}
			gj, _ := json.Marshal(got.Message)
			wj, _ := json.Marshal(want.Message)
			if !jsonEqual(gj, wj) {
				t.Errorf("stream message differs from chat:\n stream %s\n   chat %s", gj, wj)
			}
		})
	}
}

// jsonEqual compares JSON values, ignoring key order and whitespace (a tool's
// streamed input is re-serialised).
func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func (c contract) streamTerminal(t *testing.T) {
	evs := c.stream(t, Scenario{Reply: textReply("abcdef")})
	terminals := 0
	for i, e := range evs {
		if e.Terminal() {
			terminals++
			if i != len(evs)-1 {
				t.Errorf("event %d is terminal but %d events follow", i, len(evs)-1-i)
			}
		}
	}
	if terminals != 1 {
		t.Fatalf("%d terminal events, want exactly 1", terminals)
	}
	if evs[0].Type != llm.EventMessageStart {
		t.Errorf("first event = %q, want message_start", evs[0].Type)
	}
}

func (c contract) streamDeltas(t *testing.T) {
	evs := c.stream(t, Scenario{Reply: textReply("hello there")})
	var sb strings.Builder
	for _, e := range evs {
		if e.Type == llm.EventTextDelta {
			sb.WriteString(e.Text)
		}
	}
	if sb.String() != "hello there" {
		t.Fatalf("deltas = %q", sb.String())
	}
}

func (c contract) streamCancel(t *testing.T) {
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := c.driver(t, Scenario{Stall: true}).Stream(ctx, c.req())
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(50*time.Millisecond, cancel)
	var last llm.StreamEvent
	deadline := time.After(5 * time.Second)
loop:
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				break loop
			}
			last = e
		case <-deadline:
			t.Fatal("stream did not end after cancel")
		}
	}
	if last.Type != llm.EventError || !errors.Is(last.Err, context.Canceled) {
		t.Fatalf("last event = %+v, want an error wrapping context.Canceled", last)
	}
	if !settles(before) {
		t.Errorf("goroutines leaked: %d before, %d after", before, runtime.NumGoroutine())
	}
}

// A consumer that reads the first event and then cancels must still see the
// stream end with a terminal event (an error wrapping context.Canceled, or the
// message_stop if the answer was already complete) and a closed channel.
func (c contract) streamCancelMidway(t *testing.T) {
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := c.driver(t, Scenario{Reply: textReply("a fairly long answer that streams in pieces")}).Stream(ctx, c.req())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no first event")
	}
	cancel()
	var last llm.StreamEvent
	deadline := time.After(5 * time.Second)
loop:
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				break loop
			}
			last = e
		case <-deadline:
			t.Fatal("stream did not end after cancel")
		}
	}
	if !last.Terminal() {
		t.Fatalf("stream closed after cancel without a terminal event (last = %+v)", last)
	}
	if last.Type == llm.EventError && !errors.Is(last.Err, context.Canceled) {
		t.Fatalf("terminal error = %v, want context.Canceled", last.Err)
	}
	if !settles(before) {
		t.Errorf("goroutines leaked: %d before, %d after", before, runtime.NumGoroutine())
	}
}

// settles waits briefly for the goroutine count to return to its baseline.
func settles(before int) bool {
	for i := 0; i < 100; i++ {
		if runtime.NumGoroutine() <= before {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func (c contract) chatCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := c.driver(t, Scenario{Stall: true})
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err := d.Chat(ctx, c.req())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("Chat ignored cancellation")
	}
}

var providerErrors = []core.ProviderError{
	{Provider: "p", Kind: core.KindRateLimit, Status: 429, RetryAfter: 3 * time.Second, Message: "slow down"},
	{Provider: "p", Kind: core.KindStatus, Status: 503, Message: "overloaded"},
	{Provider: "p", Kind: core.KindInvalid, Status: 400, Message: "bad"},
	{Provider: "p", Kind: core.KindAuth, Status: 401, Message: "no"},
	{Provider: "p", Kind: core.KindNotFound, Status: 404, Message: "model"},
}

func checkTyped(t *testing.T, err error, want core.ProviderError) {
	t.Helper()
	var pe *core.ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v (%T), want *core.ProviderError", err, err)
	}
	if pe.Kind != want.Kind || pe.Status != want.Status {
		t.Errorf("kind/status = %s/%d, want %s/%d", pe.Kind, pe.Status, want.Kind, want.Status)
	}
	if pe.Retryable() != want.Retryable() || pe.Failover() != want.Failover() {
		t.Errorf("retry/failover = %v/%v, want %v/%v", pe.Retryable(), pe.Failover(), want.Retryable(), want.Failover())
	}
	if want.RetryAfter > 0 && pe.RetryAfter != want.RetryAfter {
		t.Errorf("RetryAfter = %v, want %v", pe.RetryAfter, want.RetryAfter)
	}
}

func (c contract) typedErrors(t *testing.T) {
	for _, want := range providerErrors {
		want := want
		t.Run(string(want.Kind), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := c.driver(t, Scenario{Err: &want}).Chat(ctx, c.req())
			checkTyped(t, err, want)
		})
	}
}

func (c contract) typedStreamErrors(t *testing.T) {
	for _, want := range providerErrors {
		want := want
		t.Run(string(want.Kind), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ch, err := c.driver(t, Scenario{Err: &want}).Stream(ctx, c.req())
			if err == nil {
				_, err = llm.Collect(ctx, ch)
			}
			checkTyped(t, err, want)
		})
	}
}

func (c contract) rejectsInvalid(t *testing.T) {
	ok := core.UserText("hi")
	bad := map[string]llm.ChatRequest{
		"no model":          {Messages: []core.Message{ok}},
		"no messages":       {Model: c.h.Model()},
		"system role":       {Model: c.h.Model(), Messages: []core.Message{{Role: core.RoleSystem, Content: core.Text("x")}, ok}},
		"orphan result":     {Model: c.h.Model(), Messages: []core.Message{{Role: core.RoleUser, Content: core.Content{core.ToolResultBlock{ToolUseID: "nope", Content: core.Text("x")}}}}},
		"ends on assistant": {Model: c.h.Model(), Messages: []core.Message{ok, core.AssistantText("hi")}},
		"unknown effort":    {Model: c.h.Model(), Messages: []core.Message{ok}, Effort: "ludicrous"},
		"duplicate tool": {Model: c.h.Model(), Messages: []core.Message{ok}, Tools: []core.ToolDef{
			{Name: "a", InputSchema: map[string]any{"type": "object"}}, {Name: "a", InputSchema: map[string]any{"type": "object"}}}},
	}
	for name, r := range bad {
		r := r
		t.Run(name, func(t *testing.T) {
			d := c.driver(t, Scenario{Reply: textReply("should never be reached")})
			if _, err := d.Chat(context.Background(), r); !errors.Is(err, llm.ErrInvalidRequest) {
				t.Errorf("Chat err = %v, want ErrInvalidRequest", err)
			}
			if _, err := d.Stream(context.Background(), r); !errors.Is(err, llm.ErrInvalidRequest) {
				t.Errorf("Stream err = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func (c contract) forcedToolChoice(t *testing.T) {
	tool := core.ToolDef{Name: "record", InputSchema: map[string]any{"type": "object"}}
	for _, mode := range []llm.ToolChoice{{Mode: llm.ToolAny}, {Mode: llm.ToolOne, Name: "record"}} {
		r := c.req()
		r.Tools = []core.ToolDef{tool}
		r.ToolChoice = mode
		_, err := c.driver(t, Scenario{Reply: toolReply()}).Chat(context.Background(), r)
		if c.h.ForcedToolChoice() && err != nil {
			t.Errorf("%s rejected on a model that supports it: %v", mode.Mode, err)
		}
		if !c.h.ForcedToolChoice() && !errors.Is(err, llm.ErrInvalidRequest) {
			t.Errorf("%s accepted on a model that does not support it: %v", mode.Mode, err)
		}
	}
	// tool choice naming a tool that is not offered is always invalid
	r := c.req()
	r.Tools = []core.ToolDef{tool}
	r.ToolChoice = llm.ToolChoice{Mode: llm.ToolOne, Name: "other"}
	if _, err := c.driver(t, Scenario{Reply: toolReply()}).Chat(context.Background(), r); !errors.Is(err, llm.ErrInvalidRequest) {
		t.Errorf("unknown named tool accepted: %v", err)
	}
}

func (c contract) doesNotMutate(t *testing.T) {
	msgs := []core.Message{
		core.UserText("one"),
		{Role: core.RoleAssistant, Content: core.Content{
			core.ThinkingBlock{Thinking: "t", Signature: "s"},
			core.ToolUseBlock{ID: "t1", Name: "x", Input: json.RawMessage(`{}`)},
		}},
		{Role: core.RoleUser, Content: core.Content{core.ToolResultBlock{ToolUseID: "t1", Content: core.Text("r")}}},
	}
	r := c.req(msgs...)
	r.Tools = []core.ToolDef{{Name: "x", InputSchema: map[string]any{"type": "object"}}}
	before, _ := json.Marshal(r)
	if _, err := c.driver(t, Scenario{Reply: textReply("ok")}).Chat(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatalf("request mutated:\n before %s\n  after %s", before, after)
	}
}

func (c contract) countTokens(t *testing.T) {
	n, err := c.driver(t, Scenario{Reply: textReply("x")}).CountTokens(context.Background(), c.req())
	if errors.Is(err, llm.ErrUnsupported) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if n <= 0 {
		t.Fatalf("CountTokens = %d", n)
	}
}

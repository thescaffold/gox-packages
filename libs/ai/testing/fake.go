// Package aitesting holds the fakes and contract suites of the AI runtime
// (TRD §6.2, PLAN M1-26): a scripted ProviderDriver, in-memory implementations
// of every port, and RunDriverContract, which every real driver must pass.
package aitesting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

// Turn is one scripted model reply. Exactly one of Reply, Err or Stall is used.
type Turn struct {
	Reply *core.ChatResponse
	// Err fails the call with this error.
	Err error
	// Stall blocks until the context is cancelled (a hung provider).
	Stall bool
	// Delay waits before answering, honouring cancellation.
	Delay time.Duration
}

// FakeModel is the model id a Fake serves by default.
const FakeModel = "fake-1"

// Fake is a scripted ProviderDriver. Each Chat or Stream consumes the next
// Turn; when the script runs out the last turn repeats, so a looping agent
// test does not have to pad it. It is safe for concurrent use.
type Fake struct {
	// Info is what Models reports; defaults to a capable model named FakeModel.
	Info *llm.ModelInfo

	mu    sync.Mutex
	turns []Turn
	calls []llm.ChatRequest
	n     int
}

// NewFake returns a driver that plays turns in order.
func NewFake(turns ...Turn) *Fake { return &Fake{turns: turns} }

// Calls returns a copy of every request made, oldest first.
func (f *Fake) Calls() []llm.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]llm.ChatRequest(nil), f.calls...)
}

// CallCount is how many calls (Chat or Stream) have been made.
func (f *Fake) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *Fake) info() llm.ModelInfo {
	if f.Info != nil {
		return *f.Info
	}
	return llm.ModelInfo{
		ID: FakeModel, Provider: "fake", ContextWindow: 200_000, MaxOutputTokens: 64_000,
		SupportsTools: true, SupportsStreaming: true, SupportsThinking: true,
		SupportsForcedToolChoice: true, SupportsStrictTools: true, ZeroRetentionOK: true,
	}
}

func (f *Fake) Provider() string { return "fake" }

func (f *Fake) Models(ctx context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{f.info()}, ctx.Err()
}

func (f *Fake) CountTokens(ctx context.Context, r llm.ChatRequest) (int64, error) {
	if err := llm.ValidateRequest(f.info(), r); err != nil {
		return 0, err
	}
	// a stable estimate: 4 characters per token over the serialised request
	b, _ := json.Marshal(struct {
		S []llm.SystemBlock
		M []core.Message
		T []core.ToolDef
	}{r.System, r.Messages, r.Tools})
	return int64(len(b)/4 + 1), nil
}

// next validates, records and returns the turn to play.
func (f *Fake) next(r llm.ChatRequest) (Turn, error) {
	info := f.info()
	if f.Info == nil {
		info.ID = r.Model // a default Fake serves whichever model is asked for
	}
	if err := llm.ValidateRequest(info, r); err != nil {
		return Turn{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, cloneRequest(r))
	if len(f.turns) == 0 {
		return Turn{}, errors.New("aitesting: Fake has no scripted turns")
	}
	i := f.n
	if i >= len(f.turns) {
		i = len(f.turns) - 1
	}
	f.n++
	return f.turns[i], nil
}

func cloneRequest(r llm.ChatRequest) llm.ChatRequest {
	r.Messages = append([]core.Message(nil), r.Messages...)
	r.System = append([]llm.SystemBlock(nil), r.System...)
	r.Tools = append([]core.ToolDef(nil), r.Tools...)
	return r
}

func wait(ctx context.Context, t Turn) error {
	if t.Delay > 0 {
		select {
		case <-time.After(t.Delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if t.Stall {
		<-ctx.Done()
		return ctx.Err()
	}
	return ctx.Err()
}

func (f *Fake) Chat(ctx context.Context, r llm.ChatRequest) (*core.ChatResponse, error) {
	t, err := f.next(r)
	if err != nil {
		return nil, err
	}
	if err := wait(ctx, t); err != nil {
		return nil, err
	}
	if t.Err != nil {
		return nil, t.Err
	}
	if t.Reply == nil {
		return nil, errors.New("aitesting: turn has no reply")
	}
	out := *t.Reply
	if out.Model == "" {
		out.Model = f.info().ID
	}
	if out.Provider == "" {
		out.Provider = "fake"
	}
	return &out, nil
}

func (f *Fake) Stream(ctx context.Context, r llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	t, err := f.next(r)
	if err != nil {
		return nil, err
	}
	ch := make(chan llm.StreamEvent)
	send := func(e llm.StreamEvent) bool {
		select {
		case ch <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}
	go func() {
		defer close(ch)
		fail := func(err error) {
			// the terminal event must always arrive, even when ctx is cancelled
			select {
			case ch <- llm.StreamEvent{Type: llm.EventError, Err: err}:
			case <-time.After(time.Second):
			}
		}
		if err := wait(ctx, t); err != nil {
			fail(err)
			return
		}
		if t.Err != nil {
			fail(t.Err)
			return
		}
		if t.Reply == nil {
			fail(errors.New("aitesting: turn has no reply"))
			return
		}
		resp := *t.Reply
		if resp.Model == "" {
			resp.Model = f.info().ID
		}
		if resp.Provider == "" {
			resp.Provider = "fake"
		}
		var acc llm.Accumulator
		emit := func(e llm.StreamEvent) bool {
			if err := acc.Add(e); err != nil {
				fail(err)
				return false
			}
			if !send(e) {
				fail(ctx.Err())
				return false
			}
			return true
		}
		if !emit(llm.StreamEvent{Type: llm.EventMessageStart, Model: resp.Model}) {
			return
		}
		for i, b := range resp.Message.Content {
			if !emitBlock(emit, i, b) {
				return
			}
		}
		u := resp.Usage
		if !emit(llm.StreamEvent{Type: llm.EventMessageDelta, StopReason: resp.StopReason, Usage: &u}) {
			return
		}
		acc.SetProviderID(resp.ProviderID)
		final, err := acc.Response()
		if err != nil {
			fail(err)
			return
		}
		final.Provider = resp.Provider
		final.InvalidToolInputs = resp.InvalidToolInputs
		if !send(llm.StreamEvent{Type: llm.EventMessageStop, Response: final}) {
			fail(ctx.Err())
		}
	}()
	return ch, nil
}

func emitBlock(emit func(llm.StreamEvent) bool, i int, b core.Block) bool {
	switch v := b.(type) {
	case core.TextBlock:
		if !emit(llm.StreamEvent{Type: llm.EventBlockStart, Index: i, BlockType: core.TypeText}) {
			return false
		}
		for _, part := range split(v.Text) {
			if !emit(llm.StreamEvent{Type: llm.EventTextDelta, Index: i, Text: part}) {
				return false
			}
		}
	case core.ThinkingBlock:
		if !emit(llm.StreamEvent{Type: llm.EventBlockStart, Index: i, BlockType: core.TypeThinking}) {
			return false
		}
		for _, part := range split(v.Thinking) {
			if !emit(llm.StreamEvent{Type: llm.EventThinkingDelta, Index: i, Text: part}) {
				return false
			}
		}
		if v.Signature != "" && !emit(llm.StreamEvent{Type: llm.EventSignature, Index: i, Text: v.Signature}) {
			return false
		}
	case core.RedactedThinkingBlock:
		if !emit(llm.StreamEvent{Type: llm.EventBlockStart, Index: i, BlockType: core.TypeRedactedThinking, Data: v.Data}) {
			return false
		}
	case core.ToolUseBlock:
		if !emit(llm.StreamEvent{Type: llm.EventBlockStart, Index: i, BlockType: core.TypeToolUse, ToolID: v.ID, ToolName: v.Name}) {
			return false
		}
		for _, part := range split(string(v.Input)) {
			if !emit(llm.StreamEvent{Type: llm.EventToolInput, Index: i, PartialJSON: part}) {
				return false
			}
		}
	default:
		// the model never produces these; nothing to stream
		return true
	}
	return emit(llm.StreamEvent{Type: llm.EventBlockStop, Index: i})
}

// split cuts s into two chunks so a test exercises reassembly; a one-character
// or empty string stays whole.
func split(s string) []string {
	if len(s) < 2 {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	// cut on a rune boundary
	cut := len(s) / 2
	for cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut++
	}
	if cut >= len(s) {
		return []string{s}
	}
	return []string{s[:cut], s[cut:]}
}

// Reply builds a scripted text reply.
func Reply(text string) Turn {
	return Turn{Reply: &core.ChatResponse{
		Message:    core.AssistantText(text),
		StopReason: core.StopEndTurn,
		Usage:      core.Usage{InputTokens: 10, OutputTokens: 5},
	}}
}

// ToolCall builds a scripted reply that calls one tool.
func ToolCall(id, name string, input any) Turn {
	raw, err := json.Marshal(input)
	if err != nil {
		panic(fmt.Sprintf("aitesting: tool input: %v", err))
	}
	return Turn{Reply: &core.ChatResponse{
		Message: core.Message{Role: core.RoleAssistant, Content: core.Content{
			core.ToolUseBlock{ID: id, Name: name, Input: raw},
		}},
		StopReason: core.StopToolUse,
		Usage:      core.Usage{InputTokens: 10, OutputTokens: 8},
	}}
}

// Fail builds a scripted failure.
func Fail(err error) Turn { return Turn{Err: err} }

// Text joins the plain text of a response, for assertions.
func Text(r *core.ChatResponse) string { return strings.TrimSpace(r.Message.Content.PlainText()) }

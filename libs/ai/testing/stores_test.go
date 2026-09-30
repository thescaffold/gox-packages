package aitesting_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	aitesting "github.com/thescaffold/gox-packages/libs/ai/testing"
)

func TestMemoryImplementsThePorts(t *testing.T) {
	m := aitesting.NewMemory()
	var (
		_ core.CredentialStore    = m
		_ core.UsageSink          = m
		_ core.MessageStore       = m
		_ core.ResponseCache      = m.Cache()
		_ core.AgentStepStore     = m.Steps()
		_ core.AgentToolCallStore = m.ToolCalls()
	)
	_ = m
}

func TestMemoryCredentials(t *testing.T) {
	m := aitesting.NewMemory()
	m.Creds["k"] = map[string]string{"apiKey": "sk-1"}
	got, err := m.Get(context.Background(), "k")
	if err != nil || got["apiKey"] != "sk-1" {
		t.Fatal(got, err)
	}
	got["apiKey"] = "changed" // a copy: the store is untouched
	again, _ := m.Get(context.Background(), "k")
	if again["apiKey"] != "sk-1" {
		t.Fatal("store handed out its own map")
	}
	if _, err := m.Get(context.Background(), "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestMemoryUsageIsIdempotentOnID(t *testing.T) {
	m := aitesting.NewMemory()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = m.Record(ctx, core.UsageEvent{ID: "u1", Cost: 5})
	}
	_ = m.Record(ctx, core.UsageEvent{ID: "u2", Cost: 7})
	_ = m.Record(ctx, core.UsageEvent{Cost: 1}) // no ID: always recorded
	_ = m.Record(ctx, core.UsageEvent{Cost: 1})
	if n := len(m.Usage()); n != 4 {
		t.Fatalf("%d events, want 4", n)
	}
}

func TestMemoryMessagesAreAppendOnlyAndValidated(t *testing.T) {
	m := aitesting.NewMemory()
	ctx := context.Background()
	if err := m.Append(ctx, "r", core.Message{Role: "bogus", Content: core.Text("x")}); err == nil {
		t.Fatal("invalid message stored")
	}
	_ = m.Append(ctx, "r", core.UserText("one"))
	_ = m.Append(ctx, "r", core.AssistantText("two"))
	_ = m.Append(ctx, "other", core.UserText("x"))
	list, _ := m.List(ctx, "r")
	if len(list) != 2 || list[0].Content.PlainText() != "one" || list[1].Content.PlainText() != "two" {
		t.Fatalf("%+v", list)
	}
	if list[0].At.IsZero() {
		t.Fatal("store did not stamp the time")
	}
	list[0] = core.UserText("tampered") // the returned slice is a copy
	again, _ := m.List(ctx, "r")
	if again[0].Content.PlainText() != "one" {
		t.Fatal("List exposed internal storage")
	}
}

func TestMemoryStepsAndCallsUpsertAndStatus(t *testing.T) {
	m := aitesting.NewMemory()
	ctx := context.Background()
	s := m.Steps()
	_ = s.Save(ctx, core.StepRecord{ID: "s1", RunID: "r", Index: 0, StopReason: core.StopToolUse})
	_ = s.Save(ctx, core.StepRecord{ID: "s2", RunID: "r", Index: 1})
	_ = s.Save(ctx, core.StepRecord{ID: "s1", RunID: "r", Index: 0, StopReason: core.StopEndTurn}) // resume re-saves
	steps, _ := s.List(ctx, "r")
	if len(steps) != 2 || steps[0].StopReason != core.StopEndTurn {
		t.Fatalf("%+v", steps)
	}
	_ = s.SetStatus(ctx, "r", core.RunBlockedCredits, "top up")
	if st, ok := m.Status("r"); !ok || st.Status != core.RunBlockedCredits || st.Detail != "top up" {
		t.Fatalf("%+v", st)
	}
	if _, ok := m.Status("none"); ok {
		t.Fatal("status for unknown run")
	}

	c := m.ToolCalls()
	_ = c.Save(ctx, core.ToolCallRecord{ID: "c1", RunID: "r", Name: "x"})
	_ = c.Save(ctx, core.ToolCallRecord{ID: "c1", RunID: "r", Name: "x", IsError: true})
	calls, _ := c.List(ctx, "r")
	if len(calls) != 1 || !calls[0].IsError {
		t.Fatalf("%+v", calls)
	}
}

func TestMemoryResponseCacheTTL(t *testing.T) {
	m := aitesting.NewMemory()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m.Now = func() time.Time { return now }
	c := m.Cache()
	ctx := context.Background()
	if r, err := c.Get(ctx, "k"); r != nil || err != nil {
		t.Fatal("miss must be (nil, nil)")
	}
	_ = c.Set(ctx, "k", &core.ChatResponse{Model: "m"}, time.Minute)
	_ = c.Set(ctx, "forever", &core.ChatResponse{Model: "f"}, 0)
	if r, _ := c.Get(ctx, "k"); r == nil || r.Model != "m" {
		t.Fatal("hit lost")
	}
	now = now.Add(time.Minute) // at expiry: gone
	if r, _ := c.Get(ctx, "k"); r != nil {
		t.Fatal("entry outlived its ttl")
	}
	if r, _ := c.Get(ctx, "forever"); r == nil {
		t.Fatal("ttl 0 must not expire")
	}
}

func TestMemoryConcurrentUse(t *testing.T) {
	m := aitesting.NewMemory()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Append(context.Background(), "r", core.UserText("x"))
			_ = m.Record(context.Background(), core.UsageEvent{})
			_ = m.Steps().Save(context.Background(), core.StepRecord{ID: core.NewID("s"), RunID: "r"})
		}()
	}
	wg.Wait()
	if l, _ := m.List(context.Background(), "r"); len(l) != 50 {
		t.Fatalf("%d messages", len(l))
	}
}

func TestFakeScriptRepeatsLastTurnAndRecordsCalls(t *testing.T) {
	f := aitesting.NewFake(aitesting.Reply("one"), aitesting.Reply("two"))
	ctx := context.Background()
	for _, want := range []string{"one", "two", "two", "two"} {
		r, err := f.Chat(ctx, chatReq())
		if err != nil {
			t.Fatal(err)
		}
		if aitesting.Text(r) != want {
			t.Fatalf("got %q want %q", aitesting.Text(r), want)
		}
	}
	if f.CallCount() != 4 || len(f.Calls()) != 4 {
		t.Fatalf("calls = %d", f.CallCount())
	}
	if _, err := aitesting.NewFake().Chat(ctx, chatReq()); err == nil {
		t.Fatal("empty script must fail loudly")
	}
}

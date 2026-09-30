package policy_test

import (
	"bytes"
	"context"
	"errors"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	"github.com/thescaffold/gox-packages/libs/ai/policy"
	aitesting "github.com/thescaffold/gox-packages/libs/ai/testing"
)

func overloaded() error {
	return &core.ProviderError{Provider: "anthropic", Kind: core.KindStatus, Status: 529, Message: "overloaded"}
}

func router(f *aitesting.Fake) *policy.Router {
	return &policy.Router{Policy: policy.Default(), Lookup: lookup, Drivers: map[string]llm.ProviderDriver{"anthropic": f}}
}

func req() llm.ChatRequest { return llm.ChatRequest{Messages: []core.Message{core.UserText("hi")}} }

func calledModels(f *aitesting.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Model)
	}
	return out
}

// PLAN M1-28 done-criterion: failover verified.
func TestChatFailsOverToTheNextModelInTheChain(t *testing.T) {
	f := aitesting.NewFake(aitesting.Fail(overloaded()), aitesting.Reply("from the fallback"))
	res, err := router(f).Chat(context.Background(), policy.TierBuild, policy.Constraints{}, req())
	if err != nil {
		t.Fatal(err)
	}
	if !eq(calledModels(f), []string{"claude-sonnet-5", "claude-opus-5"}) {
		t.Fatalf("called %v", calledModels(f))
	}
	if res.Served.Ref.Model != "claude-opus-5" || aitesting.Text(res.Response) != "from the fallback" {
		t.Fatalf("served %v %q", res.Served.Ref, aitesting.Text(res.Response))
	}
	if len(res.Attempts) != 2 || res.Attempts[0].Err == nil || res.Attempts[1].Err != nil {
		t.Fatalf("attempts %+v", res.Attempts)
	}
}

func TestChatStopsAtOnceOnAFailureTheNextModelWouldRepeat(t *testing.T) {
	for _, kind := range []core.ErrorKind{core.KindInvalid, core.KindAuth} {
		f := aitesting.NewFake(aitesting.Fail(&core.ProviderError{Provider: "anthropic", Kind: kind, Status: 400}), aitesting.Reply("never"))
		_, err := router(f).Chat(context.Background(), policy.TierBuild, policy.Constraints{}, req())
		var pe *core.ProviderError
		if !errors.As(err, &pe) || pe.Kind != kind {
			t.Fatalf("%s: %v", kind, err)
		}
		if f.CallCount() != 1 {
			t.Fatalf("%s: %d calls; a bad request must not be retried on the next model", kind, f.CallCount())
		}
	}
}

func TestChatReturnsUnavailableWhenEveryModelFails(t *testing.T) {
	f := aitesting.NewFake(aitesting.Fail(overloaded()))
	res, err := router(f).Chat(context.Background(), policy.TierBuild, policy.Constraints{}, req())
	if !errors.Is(err, policy.ErrUnavailable) {
		t.Fatalf("%v", err)
	}
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Status != 529 {
		t.Fatal("the last provider error must stay reachable")
	}
	if len(res.Attempts) != 2 {
		t.Fatalf("%+v", res.Attempts)
	}
}

func TestRefusalTriesTheNextModelThenReturnsTheLastRefusal(t *testing.T) {
	refusal := aitesting.Turn{Reply: &core.ChatResponse{Message: core.AssistantText("no"), StopReason: core.StopRefusal, Usage: core.Usage{InputTokens: 1, OutputTokens: 1}}}
	f := aitesting.NewFake(refusal, aitesting.Reply("yes"))
	r := router(f)
	r.FallbackOnRefusal = true
	res, err := r.Chat(context.Background(), policy.TierBuild, policy.Constraints{}, req())
	if err != nil || aitesting.Text(res.Response) != "yes" || !res.Attempts[0].Refused {
		t.Fatalf("%v %+v", err, res)
	}

	// every model refuses: the last refusal comes back as a normal outcome
	f = aitesting.NewFake(refusal)
	r = router(f)
	r.FallbackOnRefusal = true
	res, err = r.Chat(context.Background(), policy.TierBuild, policy.Constraints{}, req())
	if err != nil || res.Response.StopReason != core.StopRefusal || f.CallCount() != 2 {
		t.Fatalf("%v %+v calls=%d", err, res, f.CallCount())
	}

	// with the option off, the first refusal is returned without trying another
	f = aitesting.NewFake(refusal, aitesting.Reply("yes"))
	res, _ = router(f).Chat(context.Background(), policy.TierBuild, policy.Constraints{}, req())
	if res.Response.StopReason != core.StopRefusal || f.CallCount() != 1 {
		t.Fatalf("calls=%d", f.CallCount())
	}
}

func TestCircuitBreakerSkipsAFailingProviderWithoutCallingIt(t *testing.T) {
	ck := &clock{t: time.Unix(1_000, 0)}
	f := aitesting.NewFake(aitesting.Fail(overloaded()))
	r := router(f)
	r.Breaker = &policy.Breaker{Threshold: 4, Cooldown: time.Minute, Now: ck.now}
	for i := 0; i < 2; i++ { // two calls x two models = four failures
		_, _ = r.Chat(context.Background(), policy.TierBuild, policy.Constraints{}, req())
	}
	calls := f.CallCount()
	if calls != 4 {
		t.Fatalf("calls = %d", calls)
	}
	res, err := r.Chat(context.Background(), policy.TierBuild, policy.Constraints{}, req())
	if !errors.Is(err, policy.ErrUnavailable) || f.CallCount() != calls {
		t.Fatalf("an open circuit reached the provider: %v calls=%d", err, f.CallCount())
	}
	if !res.Attempts[0].Skipped || !res.Attempts[1].Skipped {
		t.Fatalf("%+v", res.Attempts)
	}
	// after the cooldown one probe goes through and a success closes the circuit
	ck.add(2 * time.Minute)
	f2 := aitesting.NewFake(aitesting.Reply("back"))
	r.Drivers["anthropic"] = f2
	if res, err := r.Chat(context.Background(), policy.TierBuild, policy.Constraints{}, req()); err != nil || aitesting.Text(res.Response) != "back" {
		t.Fatalf("%v", err)
	}
	if r.Breaker.Open("anthropic") {
		t.Fatal("circuit still open after a successful probe")
	}
}

func TestOnlyProviderHealthFailuresTripTheBreaker(t *testing.T) {
	for name, err := range map[string]error{
		"invalid":  &core.ProviderError{Provider: "anthropic", Kind: core.KindInvalid, Status: 400},
		"auth":     &core.ProviderError{Provider: "anthropic", Kind: core.KindAuth, Status: 401},
		"notfound": &core.ProviderError{Provider: "anthropic", Kind: core.KindNotFound, Status: 404},
		"plain":    errors.New("boom"),
	} {
		f := aitesting.NewFake(aitesting.Fail(err))
		r := router(f)
		r.Breaker = &policy.Breaker{Threshold: 1}
		_, _ = r.Chat(context.Background(), policy.TierBuild, policy.Constraints{}, req())
		if r.Breaker.Open("anthropic") {
			t.Errorf("%s tripped the breaker", name)
		}
	}
	// a rate limit and a connection failure do
	for _, k := range []core.ErrorKind{core.KindRateLimit, core.KindConnection} {
		f := aitesting.NewFake(aitesting.Fail(&core.ProviderError{Provider: "anthropic", Kind: k, Status: 429}))
		r := router(f)
		r.Breaker = &policy.Breaker{Threshold: 1}
		_, _ = r.Chat(context.Background(), policy.TierBuild, policy.Constraints{}, req())
		if !r.Breaker.Open("anthropic") {
			t.Errorf("%s did not trip the breaker", k)
		}
	}
}

func TestCancelledContextStopsTheChainAndIsNotCountedAgainstTheProvider(t *testing.T) {
	f := aitesting.NewFake(aitesting.Turn{Stall: true})
	r := router(f)
	r.Breaker = &policy.Breaker{Threshold: 1}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	_, err := r.Chat(ctx, policy.TierBuild, policy.Constraints{}, req())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if f.CallCount() != 1 || r.Breaker.Open("anthropic") {
		t.Fatalf("calls=%d open=%v", f.CallCount(), r.Breaker.Open("anthropic"))
	}
}

func TestChatHonoursConstraints(t *testing.T) {
	f := aitesting.NewFake(aitesting.Reply("ok"))
	_, err := router(f).Chat(context.Background(), policy.TierFrontier, policy.Constraints{ZeroRetention: true, AllowFrontier: true}, req())
	if err != nil {
		t.Fatal(err)
	}
	if !eq(calledModels(f), []string{"claude-opus-5"}) {
		t.Fatalf("a zero-retention tenant reached %v", calledModels(f))
	}
}

func TestMissingDriverIsAnError(t *testing.T) {
	r := &policy.Router{Policy: policy.Default(), Lookup: lookup, Drivers: map[string]llm.ProviderDriver{}}
	if _, err := r.Chat(context.Background(), policy.TierBuild, policy.Constraints{}, req()); err == nil {
		t.Fatal("no driver, no error")
	}
}

func drain(ch <-chan llm.StreamEvent) []llm.StreamEvent {
	var out []llm.StreamEvent
	for e := range ch {
		out = append(out, e)
	}
	return out
}

func TestStreamFailsOverWhenTheFirstEventIsAnError(t *testing.T) {
	f := aitesting.NewFake(aitesting.Fail(overloaded()), aitesting.Reply("second model"))
	ch, cand, err := router(f).Stream(context.Background(), policy.TierBuild, policy.Constraints{}, req())
	if err != nil {
		t.Fatal(err)
	}
	if cand.Ref.Model != "claude-opus-5" {
		t.Fatalf("served by %v", cand.Ref)
	}
	evs := drain(ch)
	last := evs[len(evs)-1]
	if last.Type != llm.EventMessageStop || aitesting.Text(last.Response) != "second model" {
		t.Fatalf("%+v", last)
	}
}

func TestStreamDoesNotFailOverAfterOutputWasForwarded(t *testing.T) {
	// the reply streams fine, but the consumer sees an error terminal after output:
	// emulate with a driver that yields a delta and then an error
	d := &errAfterOutput{}
	r := &policy.Router{Policy: policy.Default(), Lookup: lookup, Drivers: map[string]llm.ProviderDriver{"anthropic": d}, Breaker: &policy.Breaker{Threshold: 1}}
	ch, _, err := r.Stream(context.Background(), policy.TierBuild, policy.Constraints{}, req())
	if err != nil {
		t.Fatal(err)
	}
	evs := drain(ch)
	if evs[len(evs)-1].Type != llm.EventError || d.calls != 1 {
		t.Fatalf("calls=%d last=%+v", d.calls, evs[len(evs)-1])
	}
	if !r.Breaker.Open("anthropic") {
		t.Fatal("a mid-stream provider failure must still count against the breaker")
	}
}

type errAfterOutput struct{ calls int }

func (e *errAfterOutput) Provider() string                                { return "anthropic" }
func (e *errAfterOutput) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (e *errAfterOutput) Chat(context.Context, llm.ChatRequest) (*core.ChatResponse, error) {
	return nil, errors.New("unused")
}
func (e *errAfterOutput) CountTokens(context.Context, llm.ChatRequest) (int64, error) {
	return 0, llm.ErrUnsupported
}
func (e *errAfterOutput) Stream(ctx context.Context, r llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	e.calls++
	ch := make(chan llm.StreamEvent, 3)
	ch <- llm.StreamEvent{Type: llm.EventMessageStart, Model: r.Model}
	ch <- llm.StreamEvent{Type: llm.EventTextDelta, Text: "partial"}
	ch <- llm.StreamEvent{Type: llm.EventError, Err: overloaded()}
	close(ch)
	return ch, nil
}

func TestStreamSuccessClosesTheBreaker(t *testing.T) {
	f := aitesting.NewFake(aitesting.Reply("ok"))
	r := router(f)
	r.Breaker = &policy.Breaker{Threshold: 2}
	r.Breaker.Failure("anthropic")
	ch, _, err := r.Stream(context.Background(), policy.TierBuild, policy.Constraints{}, req())
	if err != nil {
		t.Fatal(err)
	}
	drain(ch)
	r.Breaker.Failure("anthropic") // would be the 2nd consecutive failure had the success not reset the count
	if r.Breaker.Open("anthropic") {
		t.Fatal("a completed stream did not reset the failure count")
	}
}

func TestStreamAbandonedConsumerDoesNotLeak(t *testing.T) {
	f := aitesting.NewFake(aitesting.Reply("a long enough reply to stream in pieces"))
	ctx, cancel := context.WithCancel(context.Background())
	ch, _, err := router(f).Stream(ctx, policy.TierBuild, policy.Constraints{}, req())
	if err != nil {
		t.Fatal(err)
	}
	<-ch
	cancel() // and never read again
	for i := 0; i < 150; i++ {
		var buf bytes.Buffer
		_ = pprof.Lookup("goroutine").WriteTo(&buf, 1)
		if !strings.Contains(buf.String(), "policy.(*Router).forward") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the forwarding goroutine outlived an abandoned consumer")
}

func TestStreamAllFailReturnsUnavailable(t *testing.T) {
	f := aitesting.NewFake(aitesting.Fail(overloaded()))
	_, _, err := router(f).Stream(context.Background(), policy.TierBuild, policy.Constraints{}, req())
	if !errors.Is(err, policy.ErrUnavailable) {
		t.Fatalf("%v", err)
	}
}

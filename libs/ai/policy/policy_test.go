package policy_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	"github.com/thescaffold/gox-packages/libs/ai/llm/anthropic"
	"github.com/thescaffold/gox-packages/libs/ai/policy"
	aitesting "github.com/thescaffold/gox-packages/libs/ai/testing"
)

func lookup(ref policy.ModelRef) (llm.ModelInfo, bool) {
	if ref.Provider != "anthropic" {
		return llm.ModelInfo{}, false
	}
	return anthropic.Lookup(ref.Model)
}

func models(t *testing.T, c []policy.Candidate) []string {
	t.Helper()
	var out []string
	for _, x := range c {
		out = append(out, x.Ref.Model)
	}
	return out
}

func eq(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func TestDefaultPolicyIsValidAndMatchesTheTRDTable(t *testing.T) {
	p := policy.Default()
	if err := p.Validate(lookup); err != nil {
		t.Fatal(err)
	}
	for model, want := range map[string][2]int64{
		"claude-haiku-4-5": {1_000_000, 5_000_000}, "claude-sonnet-5": {2_000_000, 10_000_000},
		"claude-opus-5": {5_000_000, 25_000_000}, "claude-fable-5-1": {10_000_000, 50_000_000},
	} {
		pr, ok := p.Price(model)
		if !ok || pr.InputPerMTok != want[0] || pr.OutputPerMTok != want[1] {
			t.Errorf("%s: %+v", model, pr)
		}
		if pr.CacheReadPerMTok != want[0]/10 || pr.CacheWritePerMTok != want[0]*5/4 {
			t.Errorf("%s cache prices: %+v", model, pr)
		}
	}
}

// PLAN M1-28 done-criterion: a zero-retention tenant never resolves Fable 5.1.
func TestZeroRetentionTenantNeverResolvesFable(t *testing.T) {
	p := policy.Default()
	for _, allowFrontier := range []bool{false, true} {
		for _, tier := range policy.Tiers {
			got, err := p.Resolve(tier, policy.Constraints{ZeroRetention: true, AllowFrontier: allowFrontier}, lookup)
			if err != nil {
				t.Fatalf("%s: %v", tier, err)
			}
			for _, m := range models(t, got) {
				if strings.HasPrefix(m, "claude-fable") {
					t.Errorf("tier %s (frontier=%v) resolved %s for a zero-retention tenant", tier, allowFrontier, m)
				}
			}
		}
	}
	// and a tenant that permits retention does get it on the frontier tier
	got, _ := p.Resolve(policy.TierFrontier, policy.Constraints{AllowFrontier: true}, lookup)
	if !eq(models(t, got), []string{"claude-fable-5-1", "claude-opus-5"}) {
		t.Fatalf("frontier chain = %v", models(t, got))
	}
}

func TestFrontierIsGatedBySpendPolicyInEveryTier(t *testing.T) {
	p := policy.Default()
	got, _ := p.Resolve(policy.TierFrontier, policy.Constraints{}, lookup)
	if !eq(models(t, got), []string{"claude-opus-5"}) {
		t.Fatalf("frontier without spend approval = %v", models(t, got))
	}
	// a frontier model placed in another tier's chain is gated too
	p = p.With(policy.Override{Chains: map[policy.Tier][]policy.ModelRef{
		policy.TierReason: {{Provider: "anthropic", Model: "claude-fable-5-1"}, {Provider: "anthropic", Model: "claude-opus-5"}},
	}})
	got, _ = p.Resolve(policy.TierReason, policy.Constraints{}, lookup)
	if !eq(models(t, got), []string{"claude-opus-5"}) {
		t.Fatalf("reason with fable in its chain = %v", models(t, got))
	}
	if !p.IsFrontier("claude-fable-5-1") || !p.IsFrontier("claude-fable-5-2") || p.IsFrontier("claude-opus-5") {
		t.Fatal("IsFrontier")
	}
}

func TestReviewerNeverSharesTheAuthorsModel(t *testing.T) {
	p := policy.Default()
	got, err := p.Resolve(policy.TierBuild, policy.Constraints{ExcludeModels: []string{"claude-sonnet-5"}}, lookup)
	if err != nil || !eq(models(t, got), []string{"claude-opus-5"}) {
		t.Fatalf("%v %v", models(t, got), err)
	}
}

func TestResolveFailsClosedWithReasons(t *testing.T) {
	p := policy.Default()
	_, err := p.Resolve(policy.TierBuild, policy.Constraints{ExcludeModels: []string{"claude-sonnet-5", "claude-opus-5"}}, lookup)
	if !errors.Is(err, policy.ErrNoModel) || !strings.Contains(err.Error(), "excluded") {
		t.Fatalf("%v", err)
	}
	// fable-only chain for a zero-retention tenant
	only := p.With(policy.Override{Chains: map[policy.Tier][]policy.ModelRef{policy.TierFrontier: {{Provider: "anthropic", Model: "claude-fable-5-1"}}}})
	_, err = only.Resolve(policy.TierFrontier, policy.Constraints{ZeroRetention: true, AllowFrontier: true}, lookup)
	if !errors.Is(err, policy.ErrNoModel) || !strings.Contains(err.Error(), "retention") {
		t.Fatalf("%v", err)
	}
	if _, err := p.Resolve("bogus", policy.Constraints{}, lookup); err == nil {
		t.Fatal("unknown tier resolved")
	}
	if _, err := p.Resolve(policy.TierBuild, policy.Constraints{}, func(policy.ModelRef) (llm.ModelInfo, bool) { return llm.ModelInfo{}, false }); !errors.Is(err, policy.ErrNoModel) {
		t.Fatalf("unknown models resolved: %v", err)
	}
}

func TestOverrideReplacesOnlyNamedTiersAndDoesNotMutateTheBase(t *testing.T) {
	base := policy.Default()
	custom := []policy.ModelRef{{Provider: "anthropic", Model: "claude-opus-5"}}
	p := base.With(policy.Override{Chains: map[policy.Tier][]policy.ModelRef{policy.TierRoutine: custom}})
	got, _ := p.Resolve(policy.TierRoutine, policy.Constraints{}, lookup)
	if !eq(models(t, got), []string{"claude-opus-5"}) {
		t.Fatalf("override ignored: %v", models(t, got))
	}
	got, _ = p.Resolve(policy.TierBuild, policy.Constraints{}, lookup)
	if !eq(models(t, got), []string{"claude-sonnet-5", "claude-opus-5"}) {
		t.Fatalf("untouched tier changed: %v", models(t, got))
	}
	got, _ = base.Resolve(policy.TierRoutine, policy.Constraints{}, lookup)
	if !eq(models(t, got), []string{"claude-haiku-4-5", "claude-sonnet-5"}) {
		t.Fatalf("base mutated: %v", models(t, got))
	}
}

func TestValidate(t *testing.T) {
	p := policy.Default()
	bad := map[string]func(*policy.Policy){
		"missing tier": func(p *policy.Policy) { delete(p.Chains, policy.TierBuild) },
		"empty chain":  func(p *policy.Policy) { p.Chains[policy.TierBuild] = nil },
		"duplicate": func(p *policy.Policy) {
			p.Chains[policy.TierBuild] = []policy.ModelRef{{Provider: "anthropic", Model: "claude-sonnet-5"}, {Provider: "anthropic", Model: "claude-sonnet-5"}}
		},
		"incomplete": func(p *policy.Policy) { p.Chains[policy.TierBuild] = []policy.ModelRef{{Model: "claude-sonnet-5"}} },
		"unknown model": func(p *policy.Policy) {
			p.Chains[policy.TierBuild] = []policy.ModelRef{{Provider: "anthropic", Model: "claude-9"}}
		},
		"no price": func(p *policy.Policy) { delete(p.Pricing, "claude-sonnet-5") },
		"unknown tier": func(p *policy.Policy) {
			p.Chains["turbo"] = []policy.ModelRef{{Provider: "anthropic", Model: "claude-opus-5"}}
		},
	}
	for name, mut := range bad {
		q := policy.Default()
		mut(&q)
		if err := q.Validate(lookup); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := p.Validate(nil); err != nil {
		t.Errorf("nil lookup must skip the existence check: %v", err)
	}
}

func TestPriceFallsBackToTheLongestPrefix(t *testing.T) {
	p := policy.Default()
	if pr, ok := p.Price("claude-sonnet-5-5"); !ok || pr.InputPerMTok != 2_000_000 {
		t.Fatalf("%+v %v", pr, ok)
	}
	if pr, ok := p.Price("claude-haiku-4-5-20251001"); !ok || pr.InputPerMTok != 1_000_000 {
		t.Fatalf("%+v %v", pr, ok)
	}
	if _, ok := p.Price("gpt-9"); ok {
		t.Fatal("unknown model priced")
	}
}

func TestPriceChoosesTheLongestMatchingPrefix(t *testing.T) {
	p := policy.Policy{Pricing: map[string]core.Pricing{
		"claude-sonnet":   {InputPerMTok: 1},
		"claude-sonnet-5": {InputPerMTok: 2},
	}}
	for i := 0; i < 50; i++ { // map order is random; the answer must not be
		if pr, ok := p.Price("claude-sonnet-5-5"); !ok || pr.InputPerMTok != 2 {
			t.Fatalf("%+v", pr)
		}
	}
}

func TestEscalate(t *testing.T) {
	want := map[policy.Tier]policy.Tier{policy.TierRoutine: policy.TierBuild, policy.TierBuild: policy.TierReason, policy.TierReason: policy.TierFrontier}
	for from, to := range want {
		if got, ok := policy.Escalate(from); !ok || got != to {
			t.Errorf("%s -> %s %v", from, got, ok)
		}
	}
	if _, ok := policy.Escalate(policy.TierFrontier); ok {
		t.Fatal("frontier escalates")
	}
	if _, ok := policy.Escalate("x"); ok {
		t.Fatal("unknown tier escalates")
	}
}

func TestCost(t *testing.T) {
	p := policy.Default()
	resp := &core.ChatResponse{Model: "claude-sonnet-5-5", Usage: core.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}}
	if c, err := p.Cost(resp); err != nil || c != 12_000_000 {
		t.Fatalf("%d %v", c, err)
	}
	if _, err := p.Cost(&core.ChatResponse{Model: "mystery"}); err == nil {
		t.Fatal("an unpriced model must be an error, not free")
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestBreakerStateMachine(t *testing.T) {
	ck := &clock{t: time.Unix(1_000, 0)}
	b := &policy.Breaker{Threshold: 3, Cooldown: 10 * time.Second, Now: ck.now}
	for i := 0; i < 2; i++ {
		b.Failure("p")
	}
	if !b.Allow("p") || b.Open("p") {
		t.Fatal("opened below the threshold")
	}
	b.Success("p") // resets the count
	b.Failure("p")
	b.Failure("p")
	if b.Open("p") {
		t.Fatal("success did not reset the count")
	}
	b.Failure("p")
	if !b.Open("p") || b.Allow("p") {
		t.Fatal("not open at the threshold")
	}
	if !b.Allow("other") {
		t.Fatal("keys are independent")
	}
	ck.add(9 * time.Second)
	if b.Allow("p") {
		t.Fatal("allowed before the cooldown")
	}
	ck.add(2 * time.Second)
	if !b.Allow("p") { // the probe
		t.Fatal("no probe after the cooldown")
	}
	if b.Allow("p") {
		t.Fatal("a second probe was admitted while one is in flight")
	}
	b.Failure("p") // failed probe: re-open for a full cooldown
	ck.add(9 * time.Second)
	if b.Allow("p") {
		t.Fatal("re-opened breaker allowed early")
	}
	ck.add(2 * time.Second)
	if !b.Allow("p") {
		t.Fatal("no probe after the second cooldown")
	}
	b.Success("p")
	if b.Open("p") || !b.Allow("p") || !b.Allow("p") {
		t.Fatal("a successful probe must close the breaker")
	}
}

func TestBreakerDefaults(t *testing.T) {
	b := &policy.Breaker{}
	for i := 0; i < 4; i++ {
		b.Failure("p")
	}
	if b.Open("p") {
		t.Fatal("default threshold is 5")
	}
	b.Failure("p")
	if !b.Open("p") {
		t.Fatal("not open at 5")
	}
}

var _ = context.Background
var _ = aitesting.NewFake

// Package policy decides which model serves a request and keeps calls flowing
// when one fails (TRD §6.2 "Model policy", PLAN M1-28).
//
// Roles name a Tier, never a model. A Policy maps each tier to an ordered chain
// of models (first is the default, the rest are fallbacks) and is data, so a
// tenant can override it. Resolve filters a chain by what the tenant allows:
//
//   - a zero-retention tenant never resolves a model that needs provider-side
//     retention (Fable 5.1 requires 30 days);
//   - frontier-class models are off unless the spend policy allows them;
//   - a reviewer excludes the model of the author it reviews.
//
// Router then runs a call along the filtered chain: a per-provider circuit
// breaker skips a provider that keeps failing, an unhealthy model fails over to
// the next, and a refusal tries the next model before failing the step.
package policy

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

// Tier is a role's need, independent of any provider.
type Tier string

const (
	// TierRoutine: summaries, titles, log triage, classification, formatting.
	TierRoutine Tier = "routine"
	// TierBuild: builders, tester, scribe, release engineer.
	TierBuild Tier = "build"
	// TierReason: analyst, architect, planner, reviewer, diagnostician, fixer.
	TierReason Tier = "reason"
	// TierFrontier: hardest incidents and escalations, gated by spend policy.
	TierFrontier Tier = "frontier"
)

// Tiers in escalation order, cheapest first.
var Tiers = []Tier{TierRoutine, TierBuild, TierReason, TierFrontier}

// Valid reports whether t is a known tier.
func (t Tier) Valid() bool {
	for _, k := range Tiers {
		if t == k {
			return true
		}
	}
	return false
}

// Escalate is the next tier up, for "cheaper first, escalate on failure or low
// confidence". The top tier has none.
func Escalate(t Tier) (Tier, bool) {
	for i, k := range Tiers {
		if k == t && i+1 < len(Tiers) {
			return Tiers[i+1], true
		}
	}
	return "", false
}

// ModelRef names a model on a provider.
type ModelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (m ModelRef) String() string { return m.Provider + "/" + m.Model }

// Policy is the configuration: chains per tier, prices, and which models are
// frontier-class. It holds no secrets and is safe to store per tenant.
type Policy struct {
	Chains map[Tier][]ModelRef `json:"chains"`
	// Pricing is keyed by model id; a dated id falls back to its longest
	// prefix ("claude-sonnet-5-5" uses "claude-sonnet-5").
	Pricing map[string]core.Pricing `json:"pricing"`
	// Frontier lists models gated by spend policy wherever they appear. An entry
	// matches a model id exactly or as a prefix.
	Frontier []string `json:"frontier"`
}

// Override replaces chains for a tenant; tiers it does not name keep the base.
type Override struct {
	Chains map[Tier][]ModelRef `json:"chains"`
}

// Default is the TRD §6.2 table. Prices are micro-USD per million tokens at the
// list prices of 2026-06-24 (config, to be re-checked before launch). Cache
// reads are priced at a tenth of input and cache writes at 1.25x input, which
// is the standard prompt-cache ratio.
func Default() Policy {
	price := func(model string, in, out int64) core.Pricing {
		return core.Pricing{Model: model, InputPerMTok: in, OutputPerMTok: out, CacheReadPerMTok: in / 10, CacheWritePerMTok: in * 5 / 4}
	}
	const a = "anthropic"
	return Policy{
		Chains: map[Tier][]ModelRef{
			TierRoutine:  {{a, "claude-haiku-4-5"}, {a, "claude-sonnet-5"}},
			TierBuild:    {{a, "claude-sonnet-5"}, {a, "claude-opus-5"}},
			TierReason:   {{a, "claude-opus-5"}, {a, "claude-sonnet-5"}},
			TierFrontier: {{a, "claude-fable-5-1"}, {a, "claude-opus-5"}},
		},
		Pricing: map[string]core.Pricing{
			"claude-haiku-4-5": price("claude-haiku-4-5", 1_000_000, 5_000_000),
			"claude-sonnet-5":  price("claude-sonnet-5", 2_000_000, 10_000_000),
			"claude-opus-5":    price("claude-opus-5", 5_000_000, 25_000_000),
			"claude-fable-5-1": price("claude-fable-5-1", 10_000_000, 50_000_000),
		},
		Frontier: []string{"claude-fable"}, // a family prefix, so a new Fable release is gated from day one
	}
}

// With returns the policy with a tenant's override applied; the receiver is not
// modified.
func (p Policy) With(o Override) Policy {
	out := Policy{Chains: map[Tier][]ModelRef{}, Pricing: p.Pricing, Frontier: p.Frontier}
	for t, c := range p.Chains {
		out.Chains[t] = c
	}
	for t, c := range o.Chains {
		out.Chains[t] = c
	}
	return out
}

// Price returns the pricing for a model id (exact, else longest prefix).
func (p Policy) Price(model string) (core.Pricing, bool) {
	if pr, ok := p.Pricing[model]; ok {
		return pr, true
	}
	best, found := core.Pricing{}, false
	for id, pr := range p.Pricing {
		if strings.HasPrefix(model, id) && (!found || len(id) > len(best.Model)) {
			best, found = pr, true
			best.Model = id
		}
	}
	return best, found
}

// IsFrontier reports whether a model is gated by spend policy.
func (p Policy) IsFrontier(model string) bool {
	for _, f := range p.Frontier {
		if model == f || strings.HasPrefix(model, f) {
			return true
		}
	}
	return false
}

// Lookup reports a model's capabilities; false means unknown.
type Lookup func(ref ModelRef) (llm.ModelInfo, bool)

// Validate checks a policy is usable: every tier present with a non-empty,
// duplicate-free chain of models that exist and have a price.
func (p Policy) Validate(lookup Lookup) error {
	var problems []string
	for _, t := range Tiers {
		chain := p.Chains[t]
		if len(chain) == 0 {
			problems = append(problems, fmt.Sprintf("tier %s has no models", t))
			continue
		}
		seen := map[ModelRef]bool{}
		for _, ref := range chain {
			if ref.Provider == "" || ref.Model == "" {
				problems = append(problems, fmt.Sprintf("tier %s has an incomplete entry", t))
				continue
			}
			if seen[ref] {
				problems = append(problems, fmt.Sprintf("tier %s lists %s twice", t, ref))
			}
			seen[ref] = true
			if lookup != nil {
				if _, ok := lookup(ref); !ok {
					problems = append(problems, fmt.Sprintf("tier %s: %s is not a known model", t, ref))
				}
			}
			if _, ok := p.Price(ref.Model); !ok {
				problems = append(problems, fmt.Sprintf("tier %s: %s has no price", t, ref))
			}
		}
	}
	for t := range p.Chains {
		if !t.Valid() {
			problems = append(problems, fmt.Sprintf("unknown tier %q", t))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New("policy: " + strings.Join(problems, "; "))
	}
	return nil
}

// Constraints are what a tenant, a spend policy or a role's rules impose.
type Constraints struct {
	// ZeroRetention: the tenant forbids provider-side retention.
	ZeroRetention bool
	// AllowFrontier: the spend policy allows frontier-class models.
	AllowFrontier bool
	// ExcludeModels: never resolve these, e.g. the author's model when choosing
	// a reviewer.
	ExcludeModels []string
}

// Candidate is one resolved model.
type Candidate struct {
	Ref     ModelRef
	Info    llm.ModelInfo
	Pricing core.Pricing
}

// ErrNoModel: no model in the tier's chain survives the constraints.
var ErrNoModel = errors.New("policy: no model available for this tier under the given constraints")

// Resolve returns the tier's chain filtered by the constraints, in order. It
// fails with ErrNoModel (wrapping the reasons) rather than return a model the
// tenant forbids.
func (p Policy) Resolve(t Tier, c Constraints, lookup Lookup) ([]Candidate, error) {
	if !t.Valid() {
		return nil, fmt.Errorf("policy: unknown tier %q", t)
	}
	excluded := map[string]bool{}
	for _, m := range c.ExcludeModels {
		excluded[m] = true
	}
	var out []Candidate
	var why []string
	for _, ref := range p.Chains[t] {
		info, ok := llm.ModelInfo{ID: ref.Model, Provider: ref.Provider}, true
		if lookup != nil {
			info, ok = lookup(ref)
		}
		switch {
		case !ok:
			why = append(why, ref.String()+": unknown model")
		case excluded[ref.Model]:
			why = append(why, ref.String()+": excluded")
		case c.ZeroRetention && !info.ZeroRetentionOK:
			why = append(why, ref.String()+": needs provider-side retention")
		case !c.AllowFrontier && p.IsFrontier(ref.Model):
			why = append(why, ref.String()+": frontier models are not allowed by the spend policy")
		default:
			pr, _ := p.Price(ref.Model)
			out = append(out, Candidate{Ref: ref, Info: info, Pricing: pr})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w (%s: %s)", ErrNoModel, t, strings.Join(why, "; "))
	}
	return out, nil
}

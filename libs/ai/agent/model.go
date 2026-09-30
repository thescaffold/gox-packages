package agent

import (
	"context"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	"github.com/thescaffold/gox-packages/libs/ai/policy"
)

// Model is what the loop streams from: one driver, or a policy-routed chain.
type Model interface {
	// Stream starts a call. The request's Model field is set by the
	// implementation when it chooses the model.
	Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamEvent, error)
	// Price costs a finished response; an error means the model is unpriced.
	Price(resp *core.ChatResponse) (core.MicroUSD, error)
}

// DriverModel calls one model on one driver.
type DriverModel struct {
	Driver  llm.ProviderDriver
	ModelID string
	Pricing core.Pricing
}

func (m DriverModel) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	req.Model = m.ModelID
	return m.Driver.Stream(ctx, req)
}

func (m DriverModel) Price(resp *core.ChatResponse) (core.MicroUSD, error) {
	return core.Cost(resp.Usage, m.Pricing), nil
}

// RoutedModel resolves a tier through a policy Router (fallbacks, breaker, constraints).
type RoutedModel struct {
	Router      *policy.Router
	Tier        policy.Tier
	Constraints policy.Constraints
}

func (m RoutedModel) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	ch, _, err := m.Router.Stream(ctx, m.Tier, m.Constraints, req)
	return ch, err
}

func (m RoutedModel) Price(resp *core.ChatResponse) (core.MicroUSD, error) {
	return m.Router.Policy.Cost(resp)
}

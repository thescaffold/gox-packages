package aitesting_test

import (
	"testing"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	aitesting "github.com/thescaffold/gox-packages/libs/ai/testing"
)

type fakeHarness struct{ forced bool }

func (fakeHarness) Model() string { return aitesting.FakeModel }

func (h fakeHarness) ForcedToolChoice() bool { return h.forced }

func (h fakeHarness) Driver(t *testing.T, sc aitesting.Scenario) llm.ProviderDriver {
	f := aitesting.NewFake()
	info := llm.ModelInfo{
		ID: aitesting.FakeModel, Provider: "fake", ContextWindow: 200_000, MaxOutputTokens: 64_000,
		SupportsTools: true, SupportsStreaming: true, SupportsThinking: true,
		SupportsForcedToolChoice: h.forced, SupportsStrictTools: true, ZeroRetentionOK: true,
	}
	f.Info = &info
	switch {
	case sc.Err != nil:
		f = aitesting.NewFake(aitesting.Fail(sc.Err))
	case sc.Stall:
		f = aitesting.NewFake(aitesting.Turn{Stall: true})
	default:
		f = aitesting.NewFake(aitesting.Turn{Reply: sc.Reply})
	}
	f.Info = &info
	return f
}

// The fake passes the same contract a real driver must.
func TestFakeDriverContract(t *testing.T) {
	t.Run("forced tool choice supported", func(t *testing.T) { aitesting.RunDriverContract(t, fakeHarness{forced: true}) })
	t.Run("forced tool choice unsupported", func(t *testing.T) { aitesting.RunDriverContract(t, fakeHarness{forced: false}) })
}

var _ core.CredentialStore = aitesting.NewMemory()

func chatReq() llm.ChatRequest {
	return llm.ChatRequest{Model: aitesting.FakeModel, Messages: []core.Message{core.UserText("hi")}}
}

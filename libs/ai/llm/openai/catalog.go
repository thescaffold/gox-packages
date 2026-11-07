package openai

import (
	"sort"
	"strings"

	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

const provider = "openai"

// families are matched by longest id prefix, so a dated release
// ("gpt-5-mini-2025-08-07") inherits its family's rules. Reasoning models
// accept reasoning.effort and return reasoning items; gpt-4.1 does neither.
// Limits are conservative and can be overridden with Config.Models.
var families = []llm.ModelInfo{
	{ID: "gpt-5", ContextWindow: 272_000, MaxOutputTokens: 128_000, SupportsTools: true, SupportsStreaming: true, SupportsVision: true,
		SupportsThinking: true, SupportsEffort: true, SupportsForcedToolChoice: true, SupportsStrictTools: true, ZeroRetentionOK: true},
	{ID: "o3", ContextWindow: 200_000, MaxOutputTokens: 100_000, SupportsTools: true, SupportsStreaming: true, SupportsVision: true,
		SupportsThinking: true, SupportsEffort: true, SupportsForcedToolChoice: true, SupportsStrictTools: true, ZeroRetentionOK: true},
	{ID: "o4-mini", ContextWindow: 200_000, MaxOutputTokens: 100_000, SupportsTools: true, SupportsStreaming: true, SupportsVision: true,
		SupportsThinking: true, SupportsEffort: true, SupportsForcedToolChoice: true, SupportsStrictTools: true, ZeroRetentionOK: true},
	{ID: "gpt-4.1", ContextWindow: 1_000_000, MaxOutputTokens: 32_768, SupportsTools: true, SupportsStreaming: true, SupportsVision: true,
		SupportsForcedToolChoice: true, SupportsStrictTools: true, ZeroRetentionOK: true},
}

// Lookup returns the capabilities for a model id, or false for an unknown
// family (the driver refuses models it has no rules for rather than guess).
func Lookup(model string) (llm.ModelInfo, bool) {
	var best llm.ModelInfo
	found := false
	for _, f := range families {
		if strings.HasPrefix(model, f.ID) && (!found || len(f.ID) > len(best.ID)) {
			best, found = f, true
		}
	}
	if !found {
		return llm.ModelInfo{}, false
	}
	best.ID = model
	best.Provider = provider
	return best, true
}

// catalogIDs are the models a tier is most likely to name.
var catalogIDs = []string{"gpt-5", "gpt-5-mini", "gpt-5-nano", "gpt-4.1", "gpt-4.1-mini"}

// Catalog lists those models with their capabilities.
func Catalog() []llm.ModelInfo {
	out := make([]llm.ModelInfo, 0, len(catalogIDs))
	for _, id := range catalogIDs {
		m, _ := Lookup(id)
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

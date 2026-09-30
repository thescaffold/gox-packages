package anthropic

import (
	"sort"
	"strings"

	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

// families are matched by longest id prefix, so a dated or point release
// ("claude-sonnet-5-5", "claude-haiku-4-5-20251001") inherits its family's
// rules. Capabilities follow TRD §6.2; context and output limits are
// deliberately conservative and can be overridden with Config.Models.
var families = []llm.ModelInfo{
	{ID: "claude-fable-5", SupportsTools: true, SupportsStreaming: true, SupportsVision: true, SupportsThinking: true, SupportsEffort: true,
		SupportsForcedToolChoice: false, SupportsStrictTools: true, ZeroRetentionOK: false},
	{ID: "claude-opus-5", SupportsTools: true, SupportsStreaming: true, SupportsVision: true, SupportsThinking: true, SupportsEffort: true,
		SupportsForcedToolChoice: true, SupportsStrictTools: true, ZeroRetentionOK: true},
	{ID: "claude-sonnet-5", SupportsTools: true, SupportsStreaming: true, SupportsVision: true, SupportsThinking: true, SupportsEffort: true,
		SupportsForcedToolChoice: true, SupportsStrictTools: true, ZeroRetentionOK: true},
	{ID: "claude-haiku-4-5", SupportsTools: true, SupportsStreaming: true, SupportsVision: true,
		SupportsForcedToolChoice: true, SupportsStrictTools: true, ZeroRetentionOK: true},
}

const (
	defaultContext   = 200_000
	defaultMaxOutput = 64_000
)

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
	best.Provider = "anthropic"
	best.ContextWindow = defaultContext
	best.MaxOutputTokens = defaultMaxOutput
	return best, true
}

// catalogIDs are the concrete models Origine's tiers name (TRD §6.2).
var catalogIDs = []string{"claude-fable-5-1", "claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5"}

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

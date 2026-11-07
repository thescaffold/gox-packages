package gemini

import (
	"sort"
	"strings"

	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

const provider = "gemini"

// families are matched by longest id prefix ("gemini-2.5-flash-lite" is its
// own family: it can turn thinking off, as Flash can and Pro can't). Only the
// Gemini 3 family takes a thinking level, so only it reports SupportsEffort.
var families = []llm.ModelInfo{
	{ID: "gemini-3", ContextWindow: 1_048_576, MaxOutputTokens: 65_536, SupportsTools: true, SupportsStreaming: true, SupportsVision: true,
		SupportsThinking: true, SupportsEffort: true, SupportsForcedToolChoice: true, ZeroRetentionOK: true},
	{ID: "gemini-2.5-pro", ContextWindow: 1_048_576, MaxOutputTokens: 65_536, SupportsTools: true, SupportsStreaming: true, SupportsVision: true,
		SupportsThinking: true, SupportsForcedToolChoice: true, ZeroRetentionOK: true},
	{ID: "gemini-2.5-flash", ContextWindow: 1_048_576, MaxOutputTokens: 65_536, SupportsTools: true, SupportsStreaming: true, SupportsVision: true,
		SupportsThinking: true, SupportsForcedToolChoice: true, ZeroRetentionOK: true},
	{ID: "gemini-2.0-flash", ContextWindow: 1_048_576, MaxOutputTokens: 8_192, SupportsTools: true, SupportsStreaming: true, SupportsVision: true,
		SupportsForcedToolChoice: true, ZeroRetentionOK: true},
}

// canDisableThinking: a zero thinking budget is accepted (Flash, Flash-Lite).
func canDisableThinking(model string) bool { return strings.HasPrefix(model, "gemini-2.5-flash") }

// usesThinkingLevel: Gemini 3 sets thinking by level, not by budget.
func usesThinkingLevel(model string) bool { return strings.HasPrefix(model, "gemini-3") }

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

var catalogIDs = []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-2.0-flash"}

// Catalog lists the models a tier is most likely to name.
func Catalog() []llm.ModelInfo {
	out := make([]llm.ModelInfo, 0, len(catalogIDs))
	for _, id := range catalogIDs {
		m, _ := Lookup(id)
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

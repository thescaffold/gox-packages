// Package deepseek is the ProviderDriver for the DeepSeek API (OpenAI-style
// Chat Completions over SSE). It always streams; Chat is Stream collected.
// deepseek-reasoner returns its reasoning as reasoning_content: it becomes a
// thinking block with no signature (DeepSeek signs nothing), and it is not sent
// back on later turns, which the API refuses.
package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	"github.com/thescaffold/gox-packages/libs/ai/llm/internal/wire"
)

const provider = "deepseek"

// DefaultBaseURL is the public API.
const DefaultBaseURL = "https://api.deepseek.com"

var catalog = []llm.ModelInfo{
	{ID: "deepseek-chat", Provider: provider, ContextWindow: 128_000, MaxOutputTokens: 8_192, SupportsTools: true, SupportsStreaming: true,
		SupportsForcedToolChoice: true, ZeroRetentionOK: true},
	{ID: "deepseek-reasoner", Provider: provider, ContextWindow: 128_000, MaxOutputTokens: 64_000, SupportsStreaming: true,
		SupportsThinking: true, ZeroRetentionOK: true},
}

// Lookup returns the capabilities for a model id.
func Lookup(model string) (llm.ModelInfo, bool) {
	for _, m := range catalog {
		if m.ID == model {
			return m, true
		}
	}
	return llm.ModelInfo{}, false
}

// Config configures a Driver.
type Config struct {
	APIKey        string
	Credentials   core.CredentialStore
	CredentialRef string
	BaseURL       string
	HTTPClient    *http.Client
	// MaxRetries for a call that fails before any output. Zero means 2; use
	// -1 to disable.
	MaxRetries int
	Models     []llm.ModelInfo
}

// Driver implements llm.ProviderDriver.
type Driver struct {
	models map[string]llm.ModelInfo
	client *wire.Client
}

var _ llm.ProviderDriver = (*Driver)(nil)

// New builds a driver.
func New(cfg Config) *Driver {
	d := &Driver{models: map[string]llm.ModelInfo{}}
	for _, m := range cfg.Models {
		m.Provider = provider
		d.models[m.ID] = m
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	d.client = &wire.Client{
		Provider: provider, BaseURL: base, HTTP: cfg.HTTPClient, MaxRetries: cfg.MaxRetries,
		Key:  wire.KeyFrom(provider, cfg.APIKey, cfg.Credentials, cfg.CredentialRef),
		Auth: func(req *http.Request, key string) { req.Header.Set("Authorization", "Bearer "+key) },
		ErrorBody: func(b []byte) (string, time.Duration) {
			var e struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(b, &e)
			return e.Error.Message, 0
		},
	}
	return d
}

func (d *Driver) Provider() string { return provider }

func (d *Driver) info(model string) (llm.ModelInfo, error) {
	if m, ok := d.models[model]; ok {
		return m, nil
	}
	if m, ok := Lookup(model); ok {
		return m, nil
	}
	return llm.ModelInfo{}, fmt.Errorf("%w: model %q is not one this driver has rules for", llm.ErrInvalidRequest, model)
}

func (d *Driver) Models(ctx context.Context) ([]llm.ModelInfo, error) {
	out := append([]llm.ModelInfo{}, catalog...)
	for _, m := range d.models {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, ctx.Err()
}

// CountTokens is not offered by the API.
func (d *Driver) CountTokens(context.Context, llm.ChatRequest) (int64, error) {
	return 0, llm.ErrUnsupported
}

// Chat runs the call as a stream and returns the assembled response.
func (d *Driver) Chat(ctx context.Context, r llm.ChatRequest) (*core.ChatResponse, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, err := d.Stream(ctx, r)
	if err != nil {
		return nil, err
	}
	return llm.Collect(ctx, ch)
}

// Stream starts the call.
func (d *Driver) Stream(ctx context.Context, r llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	info, err := d.info(r.Model)
	if err != nil {
		return nil, err
	}
	if err := llm.ValidateRequest(info, r); err != nil {
		return nil, err
	}
	body, err := buildBody(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", llm.ErrInvalidRequest, err)
	}
	resp, err := d.client.Post(ctx, "/chat/completions", body)
	if err != nil {
		return nil, wire.MapError(provider, err)
	}
	ch := make(chan llm.StreamEvent)
	go pump(ctx, resp, r.Model, ch)
	return ch, nil
}

func buildBody(r llm.ChatRequest) (map[string]any, error) {
	msgs := make([]any, 0, len(r.Messages)+1)
	if len(r.System) > 0 {
		parts := make([]string, 0, len(r.System))
		for _, s := range r.System {
			parts = append(parts, s.Text)
		}
		msgs = append(msgs, map[string]any{"role": "system", "content": strings.Join(parts, "\n\n")})
	}
	for i, m := range r.Messages {
		conv, err := convertMessage(m)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		msgs = append(msgs, conv...)
	}
	body := map[string]any{"model": r.Model, "messages": msgs, "stream": true, "stream_options": map[string]any{"include_usage": true}}
	if r.MaxTokens > 0 {
		body["max_tokens"] = r.MaxTokens
	}
	if len(r.Tools) > 0 {
		tools := make([]any, 0, len(r.Tools))
		for _, t := range r.Tools {
			fn := map[string]any{"name": t.Name, "parameters": t.InputSchema}
			if t.Description != "" {
				fn["description"] = t.Description
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		}
		body["tools"] = tools
		switch r.ToolChoice.Mode {
		case llm.ToolNone:
			body["tool_choice"] = "none"
		case llm.ToolAny:
			body["tool_choice"] = "required"
		case llm.ToolOne:
			body["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": r.ToolChoice.Name}}
		default:
			body["tool_choice"] = "auto"
		}
	}
	return body, nil
}

func convertMessage(m core.Message) ([]any, error) {
	if m.Role == core.RoleAssistant {
		var text strings.Builder
		var calls []any
		for _, b := range m.Content {
			switch v := b.(type) {
			case core.TextBlock:
				text.WriteString(v.Text)
			case core.ThinkingBlock, core.RedactedThinkingBlock:
				// reasoning is never sent back: the API refuses it in input
			case core.ToolUseBlock:
				in := v.Input
				if len(in) == 0 {
					in = json.RawMessage("{}")
				}
				calls = append(calls, map[string]any{"id": v.ID, "type": "function", "function": map[string]any{"name": v.Name, "arguments": string(in)}})
			default:
				return nil, fmt.Errorf("unsupported block %T in an assistant message", b)
			}
		}
		msg := map[string]any{"role": "assistant", "content": text.String()}
		if len(calls) > 0 {
			msg["tool_calls"] = calls
		}
		return []any{msg}, nil
	}
	var out []any
	var text []string
	for _, b := range m.Content {
		switch v := b.(type) {
		case core.ToolResultBlock:
			c := v.Content.PlainText()
			if v.IsError {
				c = "Error: " + c
			}
			out = append(out, map[string]any{"role": "tool", "tool_call_id": v.ToolUseID, "content": c})
		case core.TextBlock:
			text = append(text, v.Text)
		default:
			return nil, fmt.Errorf("deepseek takes text only; %T is not supported", b)
		}
	}
	if len(text) > 0 {
		out = append(out, map[string]any{"role": "user", "content": strings.Join(text, "\n\n")})
	}
	return out, nil
}

type chunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64  `json:"prompt_tokens"`
		CompletionTokens int64  `json:"completion_tokens"`
		CacheHit         int64  `json:"prompt_cache_hit_tokens"`
		CacheMiss        *int64 `json:"prompt_cache_miss_tokens"`
	} `json:"usage"`
}

func pump(ctx context.Context, resp *http.Response, model string, ch chan<- llm.StreamEvent) {
	defer close(ch)
	defer resp.Body.Close()
	out := &wire.Out{Provider: provider, Ctx: ctx, Ch: ch}
	sse := wire.NewSSE(resp.Body)

	var (
		started bool
		next    int
		cur     = -1
		curType string
		tools   = map[int]int{} // tool call index → block index
		finish  string
		usage   core.Usage
		done    bool
	)
	emit := func(e llm.StreamEvent) bool {
		if err := out.Emit(e); err != nil {
			out.Fail(err)
			return false
		}
		return true
	}
	closeCur := func() bool {
		if cur < 0 {
			return true
		}
		i := cur
		cur, curType = -1, ""
		return emit(llm.StreamEvent{Type: llm.EventBlockStop, Index: i})
	}
	open := func(typ string, se llm.StreamEvent) bool {
		if !closeCur() {
			return false
		}
		cur, curType = next, typ
		next++
		se.Type, se.Index, se.BlockType = llm.EventBlockStart, cur, typ
		return emit(se)
	}

	for !done {
		raw, ok := sse.Next()
		if !ok {
			break
		}
		if strings.TrimSpace(string(raw.Data)) == "[DONE]" {
			done = true
			break
		}
		var c chunk
		if err := json.Unmarshal(raw.Data, &c); err != nil {
			out.Fail(fmt.Errorf("bad stream chunk: %w", err))
			return
		}
		if !started {
			started = true
			if c.Model != "" {
				model = c.Model
			}
			out.Acc.SetProviderID(c.ID)
			if !emit(llm.StreamEvent{Type: llm.EventMessageStart, Model: model}) {
				return
			}
		}
		if u := c.Usage; u != nil {
			in := u.PromptTokens - u.CacheHit
			if u.CacheMiss != nil {
				in = *u.CacheMiss
			}
			usage = core.Usage{InputTokens: in, OutputTokens: u.CompletionTokens, CacheReadTokens: u.CacheHit}
		}
		if len(c.Choices) == 0 {
			continue
		}
		choice := c.Choices[0]
		if t := choice.Delta.ReasoningContent; t != "" {
			if curType != core.TypeThinking && !open(core.TypeThinking, llm.StreamEvent{}) {
				return
			}
			if !emit(llm.StreamEvent{Type: llm.EventThinkingDelta, Index: cur, Text: t}) {
				return
			}
		}
		if t := choice.Delta.Content; t != "" {
			if curType != core.TypeText && !open(core.TypeText, llm.StreamEvent{}) {
				return
			}
			if !emit(llm.StreamEvent{Type: llm.EventTextDelta, Index: cur, Text: t}) {
				return
			}
		}
		for _, tc := range choice.Delta.ToolCalls {
			idx, known := tools[tc.Index]
			if !known {
				if !open(core.TypeToolUse, llm.StreamEvent{ToolID: tc.ID, ToolName: tc.Function.Name}) {
					return
				}
				idx = cur
				tools[tc.Index] = idx
			}
			if tc.Function.Arguments != "" && !emit(llm.StreamEvent{Type: llm.EventToolInput, Index: idx, PartialJSON: tc.Function.Arguments}) {
				return
			}
		}
		if choice.FinishReason != "" {
			finish = choice.FinishReason
		}
	}
	if err := sse.Err(); err != nil {
		out.Fail(err)
		return
	}
	if !started || finish == "" {
		out.Fail(wire.Incomplete(provider))
		return
	}
	if !closeCur() {
		return
	}
	stop := core.StopError
	switch finish {
	case "stop":
		stop = core.StopEndTurn
	case "length":
		stop = core.StopMaxTokens
	case "tool_calls":
		stop = core.StopToolUse
	case "content_filter":
		stop = core.StopRefusal
	}
	if !emit(llm.StreamEvent{Type: llm.EventMessageDelta, StopReason: stop, Usage: &usage}) {
		return
	}
	out.Finish(model)
}

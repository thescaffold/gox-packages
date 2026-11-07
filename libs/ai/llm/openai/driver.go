// Package openai is the ProviderDriver for the OpenAI Responses API. It always
// streams, so a long call never hits an HTTP timeout; Chat is Stream
// collected. The Responses API (not Chat Completions) is used because only it
// returns reasoning that can be replayed: each reasoning item's
// encrypted_content travels in the thinking block's signature, so calls stay
// stateless (store: false).
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	"github.com/thescaffold/gox-packages/libs/ai/llm/internal/wire"
)

// DefaultBaseURL is the public API.
const DefaultBaseURL = "https://api.openai.com"

// Config configures a Driver.
type Config struct {
	// APIKey is used as is. When empty, the key is read from Credentials under
	// CredentialRef (field "apiKey") on every call. It is never logged.
	APIKey        string
	Credentials   core.CredentialStore
	CredentialRef string
	// Organization and Project are sent as headers when set.
	Organization string
	Project      string

	// BaseURL overrides the endpoint (tests; a gateway; Azure-style proxies).
	BaseURL    string
	HTTPClient *http.Client
	// MaxRetries for a call that fails before any output. Zero means 2; use
	// -1 to disable.
	MaxRetries int
	// Models overrides the built-in capability rules for specific ids.
	Models []llm.ModelInfo
}

// Driver implements llm.ProviderDriver.
type Driver struct {
	cfg    Config
	models map[string]llm.ModelInfo
	client *wire.Client
}

var _ llm.ProviderDriver = (*Driver)(nil)

// New builds a driver.
func New(cfg Config) *Driver {
	d := &Driver{cfg: cfg, models: map[string]llm.ModelInfo{}}
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
		Key: wire.KeyFrom(provider, cfg.APIKey, cfg.Credentials, cfg.CredentialRef),
		Auth: func(req *http.Request, key string) {
			req.Header.Set("Authorization", "Bearer "+key)
			if cfg.Organization != "" {
				req.Header.Set("OpenAI-Organization", cfg.Organization)
			}
			if cfg.Project != "" {
				req.Header.Set("OpenAI-Project", cfg.Project)
			}
		},
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
	out := Catalog()
	for _, m := range d.models {
		out = append(out, m)
	}
	return out, ctx.Err()
}

// CountTokens is not offered: the driver does not rely on a counting endpoint.
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

// Stream starts the call. A request rejected up front, or an HTTP error
// response, is returned as an error with no channel.
func (d *Driver) Stream(ctx context.Context, r llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	info, err := d.info(r.Model)
	if err != nil {
		return nil, err
	}
	if err := llm.ValidateRequest(info, r); err != nil {
		return nil, err
	}
	body, err := buildBody(info, r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", llm.ErrInvalidRequest, err)
	}
	resp, err := d.client.Post(ctx, "/v1/responses", body)
	if err != nil {
		return nil, wire.MapError(provider, err)
	}
	ch := make(chan llm.StreamEvent)
	go pump(ctx, resp, r.Model, ch)
	return ch, nil
}

type item struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Encrypted string `json:"encrypted_content"`
	Content   []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`
}

type wireUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

// usage: input_tokens includes cached ones; core.Usage counts them apart.
func (w wireUsage) core() core.Usage {
	return core.Usage{InputTokens: w.InputTokens - w.InputTokensDetails.CachedTokens, OutputTokens: w.OutputTokens, CacheReadTokens: w.InputTokensDetails.CachedTokens}
}

type event struct {
	Type         string `json:"type"`
	OutputIndex  int    `json:"output_index"`
	SummaryIndex int    `json:"summary_index"`
	Delta        string `json:"delta"`
	Item         item   `json:"item"`
	Code         string `json:"code"`
	Message      string `json:"message"`
	Response     struct {
		ID                string    `json:"id"`
		Model             string    `json:"model"`
		Status            string    `json:"status"`
		Usage             wireUsage `json:"usage"`
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

// block is what the pump knows about one output item.
type block struct {
	typ      string
	streamed bool // a delta arrived, so the done item's full text is not re-sent
	summary  int  // last summary index seen (reasoning)
	open     bool
}

func pump(ctx context.Context, resp *http.Response, model string, ch chan<- llm.StreamEvent) {
	defer close(ch)
	defer resp.Body.Close()
	out := &wire.Out{Provider: provider, Ctx: ctx, Ch: ch}
	sse := wire.NewSSE(resp.Body)

	var (
		started bool
		blocks  = map[int]*block{}
		refused bool
		tools   bool
	)
	emit := func(e llm.StreamEvent) bool {
		if err := out.Emit(e); err != nil {
			out.Fail(err)
			return false
		}
		return true
	}
	start := func(id, m string) bool {
		if started {
			return true
		}
		started = true
		if m != "" {
			model = m
		}
		out.Acc.SetProviderID(id)
		return emit(llm.StreamEvent{Type: llm.EventMessageStart, Model: model})
	}

	for {
		raw, ok := sse.Next()
		if !ok {
			break
		}
		if string(raw.Data) == "[DONE]" {
			continue
		}
		var ev event
		if err := json.Unmarshal(raw.Data, &ev); err != nil {
			out.Fail(fmt.Errorf("bad stream event: %w", err))
			return
		}
		switch ev.Type {
		case "response.created", "response.in_progress":
			if !start(ev.Response.ID, ev.Response.Model) {
				return
			}
		case "response.output_item.added":
			if !start("", "") {
				return
			}
			b := &block{open: true, summary: -1}
			var se llm.StreamEvent
			switch ev.Item.Type {
			case "message":
				b.typ = core.TypeText
				se = llm.StreamEvent{Type: llm.EventBlockStart, Index: ev.OutputIndex, BlockType: core.TypeText}
			case "reasoning":
				b.typ = core.TypeThinking
				se = llm.StreamEvent{Type: llm.EventBlockStart, Index: ev.OutputIndex, BlockType: core.TypeThinking}
			case "function_call":
				b.typ = core.TypeToolUse
				tools = true
				se = llm.StreamEvent{Type: llm.EventBlockStart, Index: ev.OutputIndex, BlockType: core.TypeToolUse, ToolID: ev.Item.CallID, ToolName: ev.Item.Name}
			default:
				continue // web search calls and other hosted items: nothing a caller asked for
			}
			blocks[ev.OutputIndex] = b
			if !emit(se) {
				return
			}
		case "response.output_text.delta", "response.refusal.delta":
			b := blocks[ev.OutputIndex]
			if b == nil || b.typ != core.TypeText {
				continue
			}
			if ev.Type == "response.refusal.delta" {
				refused = true
			}
			b.streamed = true
			if !emit(llm.StreamEvent{Type: llm.EventTextDelta, Index: ev.OutputIndex, Text: ev.Delta}) {
				return
			}
		case "response.reasoning_summary_text.delta":
			b := blocks[ev.OutputIndex]
			if b == nil || b.typ != core.TypeThinking {
				continue
			}
			text := ev.Delta
			if b.summary >= 0 && ev.SummaryIndex != b.summary {
				text = "\n\n" + text // a new summary part
			}
			b.summary, b.streamed = ev.SummaryIndex, true
			if !emit(llm.StreamEvent{Type: llm.EventThinkingDelta, Index: ev.OutputIndex, Text: text}) {
				return
			}
		case "response.function_call_arguments.delta":
			b := blocks[ev.OutputIndex]
			if b == nil || b.typ != core.TypeToolUse {
				continue
			}
			b.streamed = true
			if !emit(llm.StreamEvent{Type: llm.EventToolInput, Index: ev.OutputIndex, PartialJSON: ev.Delta}) {
				return
			}
		case "response.output_item.done":
			b := blocks[ev.OutputIndex]
			if b == nil || !b.open {
				continue
			}
			if !finishItem(emit, ev.OutputIndex, b, ev.Item, &refused) {
				return
			}
			b.open = false
		case "response.completed", "response.incomplete":
			if !start(ev.Response.ID, ev.Response.Model) {
				return
			}
			stop := core.StopEndTurn
			switch {
			case ev.Type == "response.incomplete" && ev.Response.IncompleteDetails.Reason == "max_output_tokens":
				stop = core.StopMaxTokens
			case ev.Type == "response.incomplete" && ev.Response.IncompleteDetails.Reason == "content_filter":
				stop = core.StopRefusal
			case ev.Type == "response.incomplete":
				stop = core.StopError
			case refused:
				stop = core.StopRefusal
			case tools:
				stop = core.StopToolUse
			}
			// blocks the API left open (a cut-off item) close now
			for idx, b := range blocks {
				if b.open {
					b.open = false
					if !emit(llm.StreamEvent{Type: llm.EventBlockStop, Index: idx}) {
						return
					}
				}
			}
			u := ev.Response.Usage.core()
			if !emit(llm.StreamEvent{Type: llm.EventMessageDelta, StopReason: stop, Usage: &u}) {
				return
			}
			out.Finish(model)
			return
		case "response.failed":
			code, msg := "server_error", "the response failed"
			if ev.Response.Error != nil {
				code, msg = ev.Response.Error.Code, ev.Response.Error.Message
			}
			out.Fail(streamError(code, msg))
			return
		case "error":
			out.Fail(streamError(ev.Code, ev.Message))
			return
		}
	}
	if err := sse.Err(); err != nil {
		out.Fail(err)
		return
	}
	out.Fail(wire.Incomplete(provider))
}

// finishItem sends what the done item carries that no delta did (the whole
// text or arguments of an item that wasn't streamed, a reasoning item's
// encrypted content) and closes the block.
func finishItem(emit func(llm.StreamEvent) bool, idx int, b *block, it item, refused *bool) bool {
	switch b.typ {
	case core.TypeText:
		if !b.streamed {
			for _, c := range it.Content {
				text := c.Text
				if c.Type == "refusal" {
					text, *refused = c.Refusal, true
				}
				if text != "" && !emit(llm.StreamEvent{Type: llm.EventTextDelta, Index: idx, Text: text}) {
					return false
				}
			}
		}
	case core.TypeThinking:
		if !b.streamed {
			for i, s := range it.Summary {
				text := s.Text
				if i > 0 {
					text = "\n\n" + text
				}
				if !emit(llm.StreamEvent{Type: llm.EventThinkingDelta, Index: idx, Text: text}) {
					return false
				}
			}
		}
		if it.Encrypted != "" {
			if !emit(llm.StreamEvent{Type: llm.EventSignature, Index: idx, Text: packSignature(it.ID, it.Encrypted)}) {
				return false
			}
		}
	case core.TypeToolUse:
		if !b.streamed && it.Arguments != "" {
			if !emit(llm.StreamEvent{Type: llm.EventToolInput, Index: idx, PartialJSON: it.Arguments}) {
				return false
			}
		}
	}
	return emit(llm.StreamEvent{Type: llm.EventBlockStop, Index: idx})
}

// streamError maps an in-stream error, where there is no HTTP status, by code.
func streamError(code, msg string) *core.ProviderError {
	e := &core.ProviderError{Provider: provider, Message: msg}
	switch code {
	case "rate_limit_exceeded", "insufficient_quota":
		e.Kind, e.Status = core.KindRateLimit, 429
	case "invalid_request_error", "invalid_prompt", "context_length_exceeded":
		e.Kind, e.Status = core.KindInvalid, 400
	case "invalid_api_key", "unauthorized":
		e.Kind, e.Status = core.KindAuth, 401
	case "model_not_found":
		e.Kind, e.Status = core.KindNotFound, 404
	default:
		e.Kind, e.Status = core.KindStatus, 500
	}
	return e
}

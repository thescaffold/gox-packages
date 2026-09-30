// Package anthropic is the ProviderDriver for the Anthropic Messages API
// (TRD §6.2, PLAN M1-27). It always streams, so a long call never hits an HTTP
// timeout; Chat is Stream collected. The SDK supplies authentication, retries
// with backoff on 429/5xx before any output, and typed HTTP errors; the request
// body is built here so each field sent is explicit.
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

// Config configures a Driver.
type Config struct {
	// APIKey is used as is. When empty, the key is read from Credentials under
	// CredentialRef (field "apiKey") on every call, so a rotated key is picked
	// up without a restart. It is never logged.
	APIKey        string
	Credentials   core.CredentialStore
	CredentialRef string

	// BaseURL overrides the API endpoint (tests; a gateway).
	BaseURL string
	// HTTPClient overrides the transport.
	HTTPClient *http.Client
	// MaxRetries is the SDK's retry count for a call that fails before any
	// output. Zero means the SDK default (2); use -1 to disable.
	MaxRetries int
	// Models overrides the built-in capability rules for specific ids.
	Models []llm.ModelInfo
}

// Driver implements llm.ProviderDriver.
type Driver struct {
	cfg    Config
	models map[string]llm.ModelInfo
}

var _ llm.ProviderDriver = (*Driver)(nil)

// New builds a driver.
func New(cfg Config) *Driver {
	d := &Driver{cfg: cfg, models: map[string]llm.ModelInfo{}}
	for _, m := range cfg.Models {
		m.Provider = provider
		d.models[m.ID] = m
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

func (d *Driver) options(ctx context.Context) ([]option.RequestOption, error) {
	key := d.cfg.APIKey
	if key == "" {
		if d.cfg.Credentials == nil {
			return nil, &core.ProviderError{Provider: provider, Kind: core.KindAuth, Message: "no API key or credential store configured"}
		}
		c, err := d.cfg.Credentials.Get(ctx, d.cfg.CredentialRef)
		if err != nil {
			return nil, &core.ProviderError{Provider: provider, Kind: core.KindAuth, Message: "credential lookup failed", Cause: err}
		}
		key = c["apiKey"]
		if key == "" {
			return nil, &core.ProviderError{Provider: provider, Kind: core.KindAuth, Message: "credential has no apiKey"}
		}
	}
	opts := []option.RequestOption{option.WithAPIKey(key)}
	if d.cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(d.cfg.BaseURL))
	}
	if d.cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(d.cfg.HTTPClient))
	}
	switch {
	case d.cfg.MaxRetries < 0:
		opts = append(opts, option.WithMaxRetries(0))
	case d.cfg.MaxRetries > 0:
		opts = append(opts, option.WithMaxRetries(d.cfg.MaxRetries))
	}
	return opts, nil
}

func (d *Driver) post(ctx context.Context, path string, body map[string]any, res any) error {
	opts, err := d.options(ctx)
	if err != nil {
		return err
	}
	client := sdk.NewClient(opts...)
	return client.Post(ctx, path, body, res)
}

// CountTokens asks the API to count the request's input tokens.
func (d *Driver) CountTokens(ctx context.Context, r llm.ChatRequest) (int64, error) {
	info, err := d.info(r.Model)
	if err != nil {
		return 0, err
	}
	if err := llm.ValidateRequest(info, r); err != nil {
		return 0, err
	}
	body, err := buildBody(info, r, true)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", llm.ErrInvalidRequest, err)
	}
	var out struct {
		InputTokens int64 `json:"input_tokens"`
	}
	if err := d.post(ctx, "/v1/messages/count_tokens", body, &out); err != nil {
		return 0, mapError(err)
	}
	return out.InputTokens, nil
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
// response, is returned as an error with no channel; anything after the stream
// opens arrives as events, ending with exactly one terminal event.
func (d *Driver) Stream(ctx context.Context, r llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	info, err := d.info(r.Model)
	if err != nil {
		return nil, err
	}
	if err := llm.ValidateRequest(info, r); err != nil {
		return nil, err
	}
	body, err := buildBody(info, r, false)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", llm.ErrInvalidRequest, err)
	}

	var resp *http.Response
	if err := d.post(ctx, "/v1/messages", body, &resp); err != nil {
		return nil, mapError(err)
	}
	dec := ssestream.NewDecoder(resp)
	if dec == nil {
		return nil, &core.ProviderError{Provider: provider, Kind: core.KindConnection, Message: "empty response body"}
	}

	ch := make(chan llm.StreamEvent)
	go d.pump(ctx, dec, r, info, ch)
	return ch, nil
}

// pump translates SSE events into StreamEvents and ends with one terminal one.
func (d *Driver) pump(ctx context.Context, dec ssestream.Decoder, r llm.ChatRequest, info llm.ModelInfo, ch chan<- llm.StreamEvent) {
	defer close(ch)
	defer dec.Close()

	terminal := func(e llm.StreamEvent) {
		select {
		case ch <- e:
		case <-time.After(time.Second): // nobody is reading; do not leak
		}
	}
	fail := func(err error) {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		terminal(llm.StreamEvent{Type: llm.EventError, Err: mapError(err)})
	}
	send := func(e llm.StreamEvent) bool {
		select {
		case ch <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}

	tools := map[string]core.ToolDef{}
	for _, t := range r.Tools {
		tools[t.Name] = t
	}

	var (
		acc      llm.Accumulator
		usage    core.Usage
		stop     core.StopReason
		toolIDs  = map[int]string{}
		model    = r.Model
		sawStart bool
	)

	for dec.Next() {
		ev := dec.Event()
		switch ev.Type {
		case "ping":
			continue
		case "message_start":
			var m struct {
				Message struct {
					ID    string `json:"id"`
					Model string `json:"model"`
					Usage wireUsage
				} `json:"message"`
			}
			if err := json.Unmarshal(ev.Data, &m); err != nil {
				fail(fmt.Errorf("bad message_start: %w", err))
				return
			}
			model = m.Message.Model
			if model == "" {
				model = r.Model
			}
			usage = m.Message.Usage.merge(usage)
			acc.SetProviderID(m.Message.ID)
			sawStart = true
			se := llm.StreamEvent{Type: llm.EventMessageStart, Model: model}
			if err := acc.Add(se); err != nil || !send(se) {
				fail(orErr(err))
				return
			}
		case "content_block_start":
			var b struct {
				Index int `json:"index"`
				Block struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
					Data string `json:"data"`
				} `json:"content_block"`
			}
			if err := json.Unmarshal(ev.Data, &b); err != nil {
				fail(fmt.Errorf("bad content_block_start: %w", err))
				return
			}
			switch b.Block.Type {
			case core.TypeText, core.TypeThinking, core.TypeRedactedThinking, core.TypeToolUse:
			default:
				fail(&core.ProviderError{Provider: provider, Kind: core.KindStatus, Status: 500,
					Message: fmt.Sprintf("unsupported content block %q in the response", b.Block.Type)})
				return
			}
			if b.Block.Type == core.TypeToolUse {
				toolIDs[b.Index] = b.Block.ID
			}
			se := llm.StreamEvent{Type: llm.EventBlockStart, Index: b.Index, BlockType: b.Block.Type, ToolID: b.Block.ID, ToolName: b.Block.Name, Data: b.Block.Data}
			if err := acc.Add(se); err != nil || !send(se) {
				fail(orErr(err))
				return
			}
		case "content_block_delta":
			var dl struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					Thinking    string `json:"thinking"`
					Signature   string `json:"signature"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal(ev.Data, &dl); err != nil {
				fail(fmt.Errorf("bad content_block_delta: %w", err))
				return
			}
			var se llm.StreamEvent
			switch dl.Delta.Type {
			case "text_delta":
				se = llm.StreamEvent{Type: llm.EventTextDelta, Index: dl.Index, Text: dl.Delta.Text}
			case "thinking_delta":
				se = llm.StreamEvent{Type: llm.EventThinkingDelta, Index: dl.Index, Text: dl.Delta.Thinking}
			case "signature_delta":
				se = llm.StreamEvent{Type: llm.EventSignature, Index: dl.Index, Text: dl.Delta.Signature}
			case "input_json_delta":
				se = llm.StreamEvent{Type: llm.EventToolInput, Index: dl.Index, PartialJSON: dl.Delta.PartialJSON}
			default:
				continue // citations and other deltas carry nothing we keep
			}
			if err := acc.Add(se); err != nil || !send(se) {
				fail(orErr(err))
				return
			}
		case "content_block_stop":
			var b struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal(ev.Data, &b); err != nil {
				fail(fmt.Errorf("bad content_block_stop: %w", err))
				return
			}
			se := llm.StreamEvent{Type: llm.EventBlockStop, Index: b.Index}
			if err := acc.Add(se); err != nil || !send(se) {
				fail(orErr(err))
				return
			}
		case "message_delta":
			var m struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage wireUsage `json:"usage"`
			}
			if err := json.Unmarshal(ev.Data, &m); err != nil {
				fail(fmt.Errorf("bad message_delta: %w", err))
				return
			}
			usage = m.Usage.merge(usage)
			stop = mapStop(m.Delta.StopReason)
			u := usage
			se := llm.StreamEvent{Type: llm.EventMessageDelta, StopReason: stop, Usage: &u}
			if err := acc.Add(se); err != nil || !send(se) {
				fail(orErr(err))
				return
			}
		case "message_stop":
			if !sawStart || stop == "" {
				fail(&core.ProviderError{Provider: provider, Kind: core.KindConnection, Message: "stream ended before the message was complete"})
				return
			}
			resp, err := acc.Response()
			if err != nil {
				fail(err)
				return
			}
			resp.Model = model
			resp.InvalidToolInputs = checkEagerInputs(resp, tools)
			terminal(llm.StreamEvent{Type: llm.EventMessageStop, Response: resp})
			return
		case "error":
			var e struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(ev.Data, &e)
			terminal(llm.StreamEvent{Type: llm.EventError, Err: streamError(e.Error.Type, e.Error.Message)})
			return
		}
	}
	// the decoder stopped without message_stop
	if err := dec.Err(); err != nil {
		fail(err)
		return
	}
	fail(&core.ProviderError{Provider: provider, Kind: core.KindConnection, Message: "stream closed before message_stop"})
}

func orErr(err error) error {
	if err != nil {
		return err
	}
	return context.Canceled
}

// checkEagerInputs validates the input of every tool call whose tool streams
// eagerly. The API stops validating such input, so an invalid one is flagged
// here and the dispatcher answers it with INVALID_JSON instead of running it.
func checkEagerInputs(resp *core.ChatResponse, tools map[string]core.ToolDef) map[string]string {
	var bad map[string]string
	for _, tu := range resp.Message.Content.ToolUses() {
		def, ok := tools[tu.Name]
		if !ok || !def.EagerInputStreaming {
			continue
		}
		if err := core.ValidateInput(def.InputSchema, tu.Input); err != nil {
			if bad == nil {
				bad = map[string]string{}
			}
			bad[tu.ID] = err.Error()
		}
	}
	return bad
}

// wireUsage is the API's usage object; input_tokens excludes cache tokens.
type wireUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// merge overlays w on prev: message_start carries the input side, and
// message_delta the running output total (and sometimes the input side again);
// a zero in w never erases a known value.
func (w wireUsage) merge(prev core.Usage) core.Usage {
	pick := func(n, old int64) int64 {
		if n != 0 {
			return n
		}
		return old
	}
	return core.Usage{
		InputTokens:      pick(w.InputTokens, prev.InputTokens),
		OutputTokens:     pick(w.OutputTokens, prev.OutputTokens),
		CacheReadTokens:  pick(w.CacheReadInputTokens, prev.CacheReadTokens),
		CacheWriteTokens: pick(w.CacheCreationInputTokens, prev.CacheWriteTokens),
	}
}

func mapStop(s string) core.StopReason {
	switch s {
	case "end_turn":
		return core.StopEndTurn
	case "max_tokens":
		return core.StopMaxTokens
	case "tool_use":
		return core.StopToolUse
	case "stop_sequence":
		return core.StopSequence
	case "refusal":
		return core.StopRefusal
	case "model_context_window_exceeded":
		return core.StopContextExceeded
	}
	return core.StopError // pause_turn (server tools, unused) and anything new
}

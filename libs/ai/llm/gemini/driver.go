// Package gemini is the ProviderDriver for the Gemini API (generativelanguage,
// streamGenerateContent over SSE). It always streams; Chat is Stream collected.
// Thought summaries become thinking blocks with their thoughtSignature; a
// signature Gemini attaches to a function call or text part becomes a thinking
// block with no text just before that part, so a replay puts it back where
// Gemini expects it.
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	"github.com/thescaffold/gox-packages/libs/ai/llm/internal/wire"
)

// DefaultBaseURL is the public API.
const DefaultBaseURL = "https://generativelanguage.googleapis.com"

// Config configures a Driver.
type Config struct {
	// APIKey is used as is. When empty, the key is read from Credentials under
	// CredentialRef (field "apiKey") on every call. It is never logged.
	APIKey        string
	Credentials   core.CredentialStore
	CredentialRef string

	BaseURL    string
	HTTPClient *http.Client
	// MaxRetries for a call that fails before any output. Zero means 2; use
	// -1 to disable.
	MaxRetries int
	Models     []llm.ModelInfo
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
		Key:       wire.KeyFrom(provider, cfg.APIKey, cfg.Credentials, cfg.CredentialRef),
		Auth:      func(req *http.Request, key string) { req.Header.Set("x-goog-api-key", key) },
		ErrorBody: errorBody,
	}
	return d
}

// errorBody reads {"error":{"message","details":[{RetryInfo, retryDelay}]}}.
func errorBody(b []byte) (string, time.Duration) {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Details []struct {
				Type       string `json:"@type"`
				RetryDelay string `json:"retryDelay"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(b, &e)
	var after time.Duration
	for _, d := range e.Error.Details {
		if strings.HasSuffix(d.Type, "RetryInfo") && d.RetryDelay != "" {
			after, _ = time.ParseDuration(d.RetryDelay)
		}
	}
	return e.Error.Message, after
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

func path(model, method string) string {
	return "/v1beta/models/" + url.PathEscape(model) + ":" + method
}

// CountTokens asks the API to count the request's input.
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
	body["model"] = "models/" + r.Model
	resp, err := d.client.Post(ctx, path(r.Model, "countTokens"), map[string]any{"generateContentRequest": body})
	if err != nil {
		return 0, wire.MapError(provider, err)
	}
	defer resp.Body.Close()
	var out struct {
		TotalTokens int64 `json:"totalTokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, wire.MapError(provider, err)
	}
	return out.TotalTokens, nil
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
	body, err := buildBody(info, r, false)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", llm.ErrInvalidRequest, err)
	}
	resp, err := d.client.Post(ctx, path(r.Model, "streamGenerateContent")+"?alt=sse", body)
	if err != nil {
		return nil, wire.MapError(provider, err)
	}
	ch := make(chan llm.StreamEvent)
	go pump(ctx, resp, r.Model, ch)
	return ch, nil
}

type part struct {
	Text         string `json:"text"`
	Thought      bool   `json:"thought"`
	Signature    string `json:"thoughtSignature"`
	FunctionCall *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
}

type chunk struct {
	ResponseID   string `json:"responseId"`
	ModelVersion string `json:"modelVersion"`
	Candidates   []struct {
		Content struct {
			Parts []part `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *struct {
		PromptTokenCount        int64 `json:"promptTokenCount"`
		CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
		CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
		ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func pump(ctx context.Context, resp *http.Response, model string, ch chan<- llm.StreamEvent) {
	defer close(ch)
	defer resp.Body.Close()
	out := &wire.Out{Provider: provider, Ctx: ctx, Ch: ch}
	sse := wire.NewSSE(resp.Body)

	var (
		started bool
		next    int    // the next block index
		cur     = -1   // the open block, or -1
		curType string // its type
		calls   int
		finish  string
		usage   core.Usage
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
	// a signature on a part that isn't a thought: an empty thinking block just before it
	signatureBlock := func(sig string) bool {
		if !open(core.TypeThinking, llm.StreamEvent{}) || !emit(llm.StreamEvent{Type: llm.EventSignature, Index: cur, Text: sig}) {
			return false
		}
		return closeCur()
	}

	for {
		raw, ok := sse.Next()
		if !ok {
			break
		}
		var c chunk
		if err := json.Unmarshal(raw.Data, &c); err != nil {
			out.Fail(fmt.Errorf("bad stream chunk: %w", err))
			return
		}
		if c.Error != nil {
			out.Fail(&core.ProviderError{Provider: provider, Kind: wire.KindForStatus(c.Error.Code), Status: c.Error.Code, Message: c.Error.Message})
			return
		}
		if !started {
			started = true
			if c.ModelVersion != "" {
				model = c.ModelVersion
			}
			out.Acc.SetProviderID(c.ResponseID)
			if !emit(llm.StreamEvent{Type: llm.EventMessageStart, Model: model}) {
				return
			}
		}
		if c.UsageMetadata != nil {
			u := c.UsageMetadata
			usage = core.Usage{InputTokens: u.PromptTokenCount - u.CachedContentTokenCount, OutputTokens: u.CandidatesTokenCount + u.ThoughtsTokenCount, CacheReadTokens: u.CachedContentTokenCount}
		}
		if c.PromptFeedback != nil && c.PromptFeedback.BlockReason != "" && len(c.Candidates) == 0 {
			finish = "SAFETY"
		}
		if len(c.Candidates) == 0 {
			continue
		}
		cand := c.Candidates[0]
		for _, p := range cand.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				if p.Signature != "" && !signatureBlock(p.Signature) {
					return
				}
				calls++
				id := p.FunctionCall.ID
				if id == "" {
					id = fmt.Sprintf("call_%d", calls)
				}
				args := string(p.FunctionCall.Args)
				if args == "" || args == "null" {
					args = "{}"
				}
				if !open(core.TypeToolUse, llm.StreamEvent{ToolID: id, ToolName: p.FunctionCall.Name}) ||
					!emit(llm.StreamEvent{Type: llm.EventToolInput, Index: cur, PartialJSON: args}) || !closeCur() {
					return
				}
			case p.Thought:
				if curType != core.TypeThinking && !open(core.TypeThinking, llm.StreamEvent{}) {
					return
				}
				if p.Text != "" && !emit(llm.StreamEvent{Type: llm.EventThinkingDelta, Index: cur, Text: p.Text}) {
					return
				}
				if p.Signature != "" && (!emit(llm.StreamEvent{Type: llm.EventSignature, Index: cur, Text: p.Signature}) || !closeCur()) {
					return
				}
			default:
				if p.Signature != "" && curType != core.TypeText {
					// the signature opens this text: it goes first
					if !signatureBlock(p.Signature) {
						return
					}
					p.Signature = ""
				}
				if p.Text != "" {
					if curType != core.TypeText && !open(core.TypeText, llm.StreamEvent{}) {
						return
					}
					if !emit(llm.StreamEvent{Type: llm.EventTextDelta, Index: cur, Text: p.Text}) {
						return
					}
				}
				if p.Signature != "" && !signatureBlock(p.Signature) { // arrived on a later piece of the text
					return
				}
			}
		}
		if cand.FinishReason != "" {
			finish = cand.FinishReason
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
	stop := mapFinish(finish)
	if stop == core.StopEndTurn && calls > 0 {
		stop = core.StopToolUse
	}
	if !emit(llm.StreamEvent{Type: llm.EventMessageDelta, StopReason: stop, Usage: &usage}) {
		return
	}
	out.Finish(model)
}

func mapFinish(s string) core.StopReason {
	switch s {
	case "STOP":
		return core.StopEndTurn
	case "MAX_TOKENS":
		return core.StopMaxTokens
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "LANGUAGE":
		return core.StopRefusal
	}
	return core.StopError // MALFORMED_FUNCTION_CALL, OTHER and anything new
}

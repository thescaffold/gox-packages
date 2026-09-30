package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	"github.com/thescaffold/gox-packages/libs/ai/llm/anthropic"
	aitesting "github.com/thescaffold/gox-packages/libs/ai/testing"
)

type captured struct {
	mu   sync.Mutex
	req  *http.Request
	body map[string]any
	key  string
}

func (c *captured) fn(r *http.Request, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.req = r.Clone(context.Background())
	c.key = r.Header.Get("x-api-key")
	c.body = map[string]any{}
	_ = json.Unmarshal(b, &c.body)
}

func driverFor(t *testing.T, model string, reply *core.ChatResponse, cap *captured, cfg anthropic.Config) *anthropic.Driver {
	srv := scenarioServer(t, model, aitesting.Scenario{Reply: reply}, cap.fn)
	cfg.BaseURL = srv.URL
	cfg.MaxRetries = -1
	return anthropic.New(cfg)
}

func ok() *core.ChatResponse {
	return &core.ChatResponse{Message: core.AssistantText("ok"), StopReason: core.StopEndTurn, Usage: core.Usage{InputTokens: 1, OutputTokens: 1}}
}

func TestRequestBodyForAFullRequest(t *testing.T) {
	var cap captured
	d := driverFor(t, "claude-opus-5", ok(), &cap, anthropic.Config{APIKey: "sk-1"})
	_, err := d.Chat(context.Background(), llm.ChatRequest{
		Model: "claude-opus-5",
		System: []llm.SystemBlock{
			{Text: "stable", Cache: &core.CacheHint{TTL: "1h"}},
			{Text: "volatile"},
		},
		Messages: []core.Message{
			core.UserText("hi"),
			{Role: core.RoleAssistant, Content: core.Content{
				core.ThinkingBlock{Thinking: "t", Signature: "sig"},
				core.RedactedThinkingBlock{Data: "red"},
				core.ToolUseBlock{ID: "tu", Name: "write", Input: json.RawMessage(`{"a":1}`)},
			}},
			{Role: core.RoleUser, Content: core.Content{
				core.ToolResultBlock{ToolUseID: "tu", IsError: true, Content: core.Text("boom")},
				core.TextBlock{Text: "go on", Cache: &core.CacheHint{}},
			}},
		},
		Tools: []core.ToolDef{
			{Name: "write", Description: "writes", InputSchema: map[string]any{"type": "object"}, EagerInputStreaming: true},
			{Name: "read", InputSchema: map[string]any{"type": "object"}},
		},
		ToolChoice: llm.ToolChoice{Mode: llm.ToolOne, Name: "read"},
		MaxTokens:  4000,
		Effort:     llm.EffortXHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	b := cap.body
	if cap.key != "sk-1" {
		t.Errorf("x-api-key = %q", cap.key)
	}
	if cap.req.URL.Path != "/v1/messages" || cap.req.Header.Get("anthropic-version") == "" {
		t.Errorf("path/version: %s %q", cap.req.URL.Path, cap.req.Header.Get("anthropic-version"))
	}
	if b["stream"] != true || b["model"] != "claude-opus-5" || b["max_tokens"] != float64(4000) {
		t.Errorf("basics: %v", b)
	}
	if got := mustJSON(b["thinking"]); got != `{"display":"omitted","type":"adaptive"}` {
		t.Errorf("thinking = %s", got)
	}
	if got := mustJSON(b["output_config"]); got != `{"effort":"xhigh"}` {
		t.Errorf("output_config = %s", got)
	}
	if got := mustJSON(b["tool_choice"]); got != `{"name":"read","type":"tool"}` {
		t.Errorf("tool_choice = %s", got)
	}
	sys := b["system"].([]any)
	if mustJSON(sys[0]) != `{"cache_control":{"ttl":"1h","type":"ephemeral"},"text":"stable","type":"text"}` || mustJSON(sys[1]) != `{"text":"volatile","type":"text"}` {
		t.Errorf("system = %s", mustJSON(sys))
	}
	tools := b["tools"].([]any)
	w, r := tools[0].(map[string]any), tools[1].(map[string]any)
	if w["strict"] != true || w["eager_input_streaming"] != true || r["strict"] != true || r["eager_input_streaming"] != nil {
		t.Errorf("tools = %s", mustJSON(tools))
	}
	msgs := b["messages"].([]any)
	if mustJSON(msgs[1]) != `{"content":[{"signature":"sig","thinking":"t","type":"thinking"},{"data":"red","type":"redacted_thinking"},{"id":"tu","input":{"a":1},"name":"write","type":"tool_use"}],"role":"assistant"}` {
		t.Errorf("assistant = %s", mustJSON(msgs[1]))
	}
	if mustJSON(msgs[2]) != `{"content":[{"content":[{"text":"boom","type":"text"}],"is_error":true,"tool_use_id":"tu","type":"tool_result"},{"cache_control":{"type":"ephemeral"},"text":"go on","type":"text"}],"role":"user"}` {
		t.Errorf("user = %s", mustJSON(msgs[2]))
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// TRD §6.2: these fields are rejected by current models with a 400.
func TestNeverSendsSamplingOrBudgetTokens(t *testing.T) {
	for _, model := range []string{"claude-fable-5-1", "claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5"} {
		var cap captured
		d := driverFor(t, model, ok(), &cap, anthropic.Config{APIKey: "k"})
		if _, err := d.Chat(context.Background(), llm.ChatRequest{Model: model, Messages: []core.Message{core.UserText("x")}, Effort: llm.EffortHigh}); err != nil {
			t.Fatal(err)
		}
		raw := mustJSON(cap.body)
		for _, banned := range []string{"temperature", "top_p", "top_k", "budget_tokens"} {
			if strings.Contains(raw, banned) {
				t.Errorf("%s: body contains %s: %s", model, banned, raw)
			}
		}
	}
}

func TestThinkingAndEffortFollowCapabilities(t *testing.T) {
	cases := []struct {
		model    string
		thinking llm.ThinkingMode
		effort   llm.Effort
		wantThk  string
		wantEff  string
	}{
		{"claude-sonnet-5", llm.ThinkingAdaptive, llm.EffortDefault, `{"display":"omitted","type":"adaptive"}`, ""},
		{"claude-sonnet-5", llm.ThinkingOff, llm.EffortLow, `{"type":"disabled"}`, `{"effort":"low"}`},
		{"claude-sonnet-5", llm.ThinkingProviderDefault, llm.EffortDefault, "", ""},
		{"claude-haiku-4-5", llm.ThinkingAdaptive, llm.EffortHigh, "", ""}, // a model without either sends neither
	}
	for _, c := range cases {
		var cap captured
		d := driverFor(t, c.model, ok(), &cap, anthropic.Config{APIKey: "k"})
		if _, err := d.Chat(context.Background(), llm.ChatRequest{Model: c.model, Messages: []core.Message{core.UserText("x")}, Thinking: c.thinking, Effort: c.effort}); err != nil {
			t.Fatal(err)
		}
		thk, eff := "", ""
		if v, ok := cap.body["thinking"]; ok {
			thk = mustJSON(v)
		}
		if v, ok := cap.body["output_config"]; ok {
			eff = mustJSON(v)
		}
		if thk != c.wantThk || eff != c.wantEff {
			t.Errorf("%s/%q/%q: thinking=%s effort=%s", c.model, c.thinking, c.effort, thk, eff)
		}
	}
}

func TestThinkingSummariesAreOptIn(t *testing.T) {
	var cap captured
	d := driverFor(t, "claude-opus-5", ok(), &cap, anthropic.Config{APIKey: "k"})
	_, err := d.Chat(context.Background(), llm.ChatRequest{Model: "claude-opus-5", Messages: []core.Message{core.UserText("x")}, Extensions: map[string]any{"thinking_display": "summarized"}})
	if err != nil {
		t.Fatal(err)
	}
	if mustJSON(cap.body["thinking"]) != `{"display":"summarized","type":"adaptive"}` {
		t.Fatalf("thinking = %s", mustJSON(cap.body["thinking"]))
	}
	d = driverFor(t, "claude-opus-5", ok(), &cap, anthropic.Config{APIKey: "k"})
	_, _ = d.Chat(context.Background(), llm.ChatRequest{Model: "claude-opus-5", Messages: []core.Message{core.UserText("x")}, Extensions: map[string]any{"thinking_display": "bogus"}})
	if mustJSON(cap.body["thinking"]) != `{"display":"omitted","type":"adaptive"}` {
		t.Fatal("an invalid display value must fall back to omitted")
	}
}

func TestDefaultMaxTokensAndToolChoiceDefault(t *testing.T) {
	var cap captured
	d := driverFor(t, "claude-sonnet-5", ok(), &cap, anthropic.Config{APIKey: "k"})
	_, err := d.Chat(context.Background(), llm.ChatRequest{Model: "claude-sonnet-5", Messages: []core.Message{core.UserText("x")},
		Tools: []core.ToolDef{{Name: "t", InputSchema: map[string]any{"type": "object"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if cap.body["max_tokens"] != float64(16000) || mustJSON(cap.body["tool_choice"]) != `{"type":"auto"}` {
		t.Fatalf("%v", cap.body)
	}
}

func TestCredentialStoreIsReadPerCallSoRotationIsPickedUp(t *testing.T) {
	var cap captured
	mem := aitesting.NewMemory()
	mem.Creds["anthropic/prod"] = map[string]string{"apiKey": "sk-old"}
	d := driverFor(t, "claude-sonnet-5", ok(), &cap, anthropic.Config{Credentials: mem, CredentialRef: "anthropic/prod"})
	req := llm.ChatRequest{Model: "claude-sonnet-5", Messages: []core.Message{core.UserText("x")}}
	if _, err := d.Chat(context.Background(), req); err != nil || cap.key != "sk-old" {
		t.Fatalf("%v key=%q", err, cap.key)
	}
	mem.Creds["anthropic/prod"] = map[string]string{"apiKey": "sk-new"}
	if _, err := d.Chat(context.Background(), req); err != nil || cap.key != "sk-new" {
		t.Fatalf("%v key=%q", err, cap.key)
	}
}

func TestMissingCredentialsAreAnAuthError(t *testing.T) {
	mem := aitesting.NewMemory()
	mem.Creds["empty"] = map[string]string{}
	for name, cfg := range map[string]anthropic.Config{
		"no key no store": {},
		"unknown ref":     {Credentials: mem, CredentialRef: "nope"},
		"no apiKey field": {Credentials: mem, CredentialRef: "empty"},
	} {
		d := anthropic.New(cfg)
		_, err := d.Chat(context.Background(), llm.ChatRequest{Model: "claude-sonnet-5", Messages: []core.Message{core.UserText("x")}})
		var pe *core.ProviderError
		if !errors.As(err, &pe) || pe.Kind != core.KindAuth {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestUnknownModelIsRejectedNotGuessed(t *testing.T) {
	d := anthropic.New(anthropic.Config{APIKey: "k"})
	_, err := d.Chat(context.Background(), llm.ChatRequest{Model: "gpt-9", Messages: []core.Message{core.UserText("x")}})
	if !errors.Is(err, llm.ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
}

func TestModelFamiliesInheritRules(t *testing.T) {
	for id, forced := range map[string]bool{"claude-fable-5-1": false, "claude-fable-5": false, "claude-sonnet-5-5": true, "claude-haiku-4-5-20251001": true, "claude-opus-5-5": true} {
		m, ok := anthropic.Lookup(id)
		if !ok || m.ID != id || m.SupportsForcedToolChoice != forced || m.Provider != "anthropic" {
			t.Errorf("%s: %+v ok=%v", id, m, ok)
		}
	}
	if m, _ := anthropic.Lookup("claude-fable-5-1"); m.ZeroRetentionOK {
		t.Error("fable must not be marked zero-retention-safe")
	}
	if m, _ := anthropic.Lookup("claude-opus-5"); !m.ZeroRetentionOK {
		t.Error("opus is zero-retention-safe")
	}
	if _, ok := anthropic.Lookup("claude-3-opus"); ok {
		t.Error("an unknown family resolved")
	}
	if len(anthropic.Catalog()) != 4 {
		t.Errorf("catalog = %d", len(anthropic.Catalog()))
	}
}

func TestConfigModelsOverrideTheBuiltIns(t *testing.T) {
	var cap captured
	custom := llm.ModelInfo{ID: "claude-sonnet-5", MaxOutputTokens: 100, SupportsTools: true}
	d := driverFor(t, "claude-sonnet-5", ok(), &cap, anthropic.Config{APIKey: "k", Models: []llm.ModelInfo{custom}})
	_, err := d.Chat(context.Background(), llm.ChatRequest{Model: "claude-sonnet-5", Messages: []core.Message{core.UserText("x")}, MaxTokens: 101})
	if !errors.Is(err, llm.ErrInvalidRequest) {
		t.Fatalf("override not applied: %v", err)
	}
}

func TestCacheAndUsageAccounting(t *testing.T) {
	reply := &core.ChatResponse{Message: core.AssistantText("x"), StopReason: core.StopEndTurn,
		Usage: core.Usage{InputTokens: 100, OutputTokens: 40, CacheReadTokens: 900, CacheWriteTokens: 50}}
	var cap captured
	d := driverFor(t, "claude-sonnet-5", reply, &cap, anthropic.Config{APIKey: "k"})
	r, err := d.Chat(context.Background(), llm.ChatRequest{Model: "claude-sonnet-5", Messages: []core.Message{core.UserText("x")}})
	if err != nil || r.Usage != reply.Usage {
		t.Fatalf("%v %+v", err, r.Usage)
	}
	if r.ProviderID != "msg_01" || r.Model != "claude-sonnet-5" {
		t.Errorf("%+v", r)
	}
}

func toolReplyFor(input string) *core.ChatResponse {
	return &core.ChatResponse{
		Message:    core.Message{Role: core.RoleAssistant, Content: core.Content{core.ToolUseBlock{ID: "tu1", Name: "write", Input: json.RawMessage(input)}}},
		StopReason: core.StopToolUse, Usage: core.Usage{InputTokens: 1, OutputTokens: 1},
	}
}

var writeTool = core.ToolDef{Name: "write", InputSchema: map[string]any{
	"type": "object", "required": []any{"path"}, "properties": map[string]any{"path": map[string]any{"type": "string"}}},
	EagerInputStreaming: true}

func TestEagerToolInputIsValidatedClientSide(t *testing.T) {
	for name, tc := range map[string]struct {
		input   string
		invalid bool
	}{
		"valid":         {`{"path":"a.go"}`, false},
		"missing field": {`{}`, true},
		"wrong type":    {`{"path":7}`, true},
	} {
		var cap captured
		d := driverFor(t, "claude-sonnet-5", toolReplyFor(tc.input), &cap, anthropic.Config{APIKey: "k"})
		r, err := d.Chat(context.Background(), llm.ChatRequest{Model: "claude-sonnet-5", Messages: []core.Message{core.UserText("x")}, Tools: []core.ToolDef{writeTool}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, flagged := r.InvalidToolInputs["tu1"]
		if flagged != tc.invalid {
			t.Errorf("%s: flagged = %v (%v)", name, flagged, r.InvalidToolInputs)
		}
		if len(r.Message.Content.ToolUses()) != 1 {
			t.Errorf("%s: the call itself must still be returned", name)
		}
	}
	// a tool that does not stream eagerly is validated by the API (strict), not here
	plain := writeTool
	plain.EagerInputStreaming = false
	var cap captured
	d := driverFor(t, "claude-sonnet-5", toolReplyFor(`{}`), &cap, anthropic.Config{APIKey: "k"})
	r, _ := d.Chat(context.Background(), llm.ChatRequest{Model: "claude-sonnet-5", Messages: []core.Message{core.UserText("x")}, Tools: []core.ToolDef{plain}})
	if len(r.InvalidToolInputs) != 0 {
		t.Errorf("non-eager tool validated client-side: %v", r.InvalidToolInputs)
	}
}

// max_tokens can cut a tool call mid-input. The outcome must be max_tokens with
// the unanswerable call dropped, never a run of a half-written call.
func TestTruncatedToolCallSurfacesAsMaxTokens(t *testing.T) {
	sseBody := `event: message_start
data: {"type":"message_start","message":{"id":"m","model":"claude-sonnet-5","usage":{"input_tokens":5,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu","name":"write","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.g"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":99}}

event: message_stop
data: {"type":"message_stop"}

`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseBody))
	}))
	defer srv.Close()
	d := anthropic.New(anthropic.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})
	r, err := d.Chat(context.Background(), llm.ChatRequest{Model: "claude-sonnet-5", Messages: []core.Message{core.UserText("x")}, Tools: []core.ToolDef{writeTool}})
	if err != nil {
		t.Fatal(err)
	}
	if r.StopReason != core.StopMaxTokens || len(r.Message.Content.ToolUses()) != 0 || r.Usage.OutputTokens != 99 {
		t.Fatalf("%+v", r)
	}
}

func TestStopReasonMapping(t *testing.T) {
	for wire, want := range map[string]core.StopReason{
		"end_turn": core.StopEndTurn, "max_tokens": core.StopMaxTokens, "stop_sequence": core.StopSequence, "refusal": core.StopRefusal,
		"model_context_window_exceeded": core.StopContextExceeded, "pause_turn": core.StopError, "something_new": core.StopError,
	} {
		reply := &core.ChatResponse{Message: core.AssistantText("x"), StopReason: core.StopReason(wire), Usage: core.Usage{InputTokens: 1, OutputTokens: 1}}
		var cap captured
		d := driverFor(t, "claude-sonnet-5", reply, &cap, anthropic.Config{APIKey: "k"})
		r, err := d.Chat(context.Background(), llm.ChatRequest{Model: "claude-sonnet-5", Messages: []core.Message{core.UserText("x")}})
		if err != nil || r.StopReason != want {
			t.Errorf("%s -> %v (%v), want %s", wire, r, err, want)
		}
	}
}

func streamServer(t *testing.T, body string) *anthropic.Driver {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return anthropic.New(anthropic.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})
}

const startEv = `event: message_start
data: {"type":"message_start","message":{"id":"m","model":"claude-sonnet-5","usage":{"input_tokens":5,"output_tokens":1}}}

`

func chatOnce(d *anthropic.Driver) (*core.ChatResponse, error) {
	return d.Chat(context.Background(), llm.ChatRequest{Model: "claude-sonnet-5", Messages: []core.Message{core.UserText("x")}})
}

func TestMidStreamErrorEventsAreTyped(t *testing.T) {
	for typ, want := range map[string]struct {
		kind   core.ErrorKind
		status int
	}{
		"overloaded_error":      {core.KindStatus, 529},
		"rate_limit_error":      {core.KindRateLimit, 429},
		"invalid_request_error": {core.KindInvalid, 400},
		"authentication_error":  {core.KindAuth, 401},
		"not_found_error":       {core.KindNotFound, 404},
		"api_error":             {core.KindStatus, 500},
		"brand_new_error":       {core.KindStatus, 500},
	} {
		d := streamServer(t, startEv+"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\""+typ+"\",\"message\":\"m\"}}\n\n")
		_, err := chatOnce(d)
		var pe *core.ProviderError
		if !errors.As(err, &pe) || pe.Kind != want.kind || pe.Status != want.status {
			t.Errorf("%s: %v", typ, err)
		}
	}
	// overloaded mid-stream is retryable and a failover trigger
	d := streamServer(t, startEv+"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"m\"}}\n\n")
	_, err := chatOnce(d)
	var pe *core.ProviderError
	_ = errors.As(err, &pe)
	if !pe.Retryable() || !pe.Failover() {
		t.Error("overloaded must be retryable + failover")
	}
}

func TestTruncatedOrMalformedStreamsFailLoudly(t *testing.T) {
	for name, body := range map[string]string{
		"closed before message_stop": startEv,
		"empty":                      "",
		"no stop reason":             startEv + "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		"bad json":                   startEv + "event: content_block_start\ndata: {nope\n\n",
		"unsupported block":          startEv + "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"server_tool_use\",\"id\":\"s\",\"name\":\"web\"}}\n\n",
	} {
		_, err := chatOnce(streamServer(t, body))
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestPingsAndUnknownDeltasAreIgnored(t *testing.T) {
	body := startEv + `event: ping
data: {"type":"ping"}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}

event: message_stop
data: {"type":"message_stop"}

`
	r, err := chatOnce(streamServer(t, body))
	if err != nil || r.Message.Content.PlainText() != "hi" || r.Usage != (core.Usage{InputTokens: 5, OutputTokens: 3}) {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestHTTPErrorsRetryAfterAndStatusKinds(t *testing.T) {
	for _, tc := range []struct {
		status int
		kind   core.ErrorKind
	}{{429, core.KindRateLimit}, {401, core.KindAuth}, {403, core.KindAuth}, {404, core.KindNotFound}, {400, core.KindInvalid}, {413, core.KindInvalid}, {422, core.KindInvalid}, {500, core.KindStatus}, {529, core.KindStatus}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "7")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"x","message":"nope"}}`))
		}))
		d := anthropic.New(anthropic.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})
		_, err := chatOnce(d)
		srv.Close()
		var pe *core.ProviderError
		if !errors.As(err, &pe) || pe.Kind != tc.kind || pe.Status != tc.status || pe.RetryAfter != 7*time.Second {
			t.Errorf("%d: %v", tc.status, err)
		}
	}
}

func TestConnectionFailureIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more
	d := anthropic.New(anthropic.Config{APIKey: "k", BaseURL: url, MaxRetries: -1})
	_, err := chatOnce(d)
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Kind != core.KindConnection || !pe.Retryable() {
		t.Fatalf("err = %v", err)
	}
}

func TestSDKRetriesTransientFailuresBeforeOutput(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Retry-After-Ms", "1")
			w.WriteHeader(529)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse("claude-sonnet-5", ok())))
	}))
	defer srv.Close()
	d := anthropic.New(anthropic.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: 2})
	if _, err := chatOnce(d); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestCountTokensRequestShape(t *testing.T) {
	var cap captured
	d := driverFor(t, "claude-sonnet-5", ok(), &cap, anthropic.Config{APIKey: "k"})
	n, err := d.CountTokens(context.Background(), llm.ChatRequest{Model: "claude-sonnet-5", System: []llm.SystemBlock{{Text: "s"}}, Messages: []core.Message{core.UserText("x")}, MaxTokens: 5, Effort: llm.EffortHigh})
	if err != nil || n != 42 {
		t.Fatalf("%d %v", n, err)
	}
	if cap.req.URL.Path != "/v1/messages/count_tokens" {
		t.Fatalf("path = %s", cap.req.URL.Path)
	}
	for _, k := range []string{"stream", "max_tokens", "thinking", "output_config"} {
		if _, has := cap.body[k]; has {
			t.Errorf("count_tokens body has %q", k)
		}
	}
	if _, has := cap.body["system"]; !has {
		t.Error("system missing")
	}
}

func TestStreamValidatesBeforeAnyHTTPCall(t *testing.T) {
	var cap captured
	d := driverFor(t, "claude-sonnet-5", ok(), &cap, anthropic.Config{APIKey: "k"})
	if _, err := d.Stream(context.Background(), llm.ChatRequest{Model: "claude-sonnet-5"}); !errors.Is(err, llm.ErrInvalidRequest) {
		t.Fatal(err)
	}
	if cap.req != nil {
		t.Fatal("a call was made for an invalid request")
	}
}

// Gated: runs against the real API when ANTHROPIC_API_KEY is set.
func TestLiveSmoke(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	model := os.Getenv("ANTHROPIC_SMOKE_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}
	d := anthropic.New(anthropic.Config{APIKey: key})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r, err := d.Chat(ctx, llm.ChatRequest{Model: model, MaxTokens: 64, Messages: []core.Message{core.UserText("Reply with the single word: pong")}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(r.Message.Content.PlainText()), "pong") || r.Usage.OutputTokens == 0 {
		t.Fatalf("%+v", r)
	}
	ch, err := d.Stream(ctx, llm.ChatRequest{Model: model, MaxTokens: 64, Messages: []core.Message{core.UserText("Say hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := llm.Collect(ctx, ch); err != nil {
		t.Fatal(err)
	}
}

func TestUsageZeroNeverErasesAKnownValue(t *testing.T) {
	body := startEv + `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{}}

event: message_stop
data: {"type":"message_stop"}

`
	r, err := chatOnce(streamServer(t, body))
	if err != nil || r.Usage != (core.Usage{InputTokens: 5, OutputTokens: 1}) {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestConfiguredRetryCountIsHonoured(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Retry-After-Ms", "1")
		w.WriteHeader(529)
	}))
	defer srv.Close()
	d := anthropic.New(anthropic.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: 1})
	if _, err := chatOnce(d); err == nil {
		t.Fatal("expected failure")
	}
	if calls != 2 { // the first try plus one retry
		t.Fatalf("calls = %d, want 2", calls)
	}
}

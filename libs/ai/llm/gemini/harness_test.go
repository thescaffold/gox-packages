package gemini_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	"github.com/thescaffold/gox-packages/libs/ai/llm/gemini"
	aitesting "github.com/thescaffold/gox-packages/libs/ai/testing"
)

// chunks renders a response as streamGenerateContent SSE chunks: text split in
// two pieces, a function call whole, usage and the finish reason on the last.
func chunks(model string, r *core.ChatResponse) string {
	var parts [][]map[string]any
	for _, blk := range r.Message.Content {
		switch v := blk.(type) {
		case core.TextBlock:
			h := len(v.Text) / 2
			parts = append(parts, []map[string]any{{"text": v.Text[:h]}}, []map[string]any{{"text": v.Text[h:]}})
		case core.ThinkingBlock:
			parts = append(parts, []map[string]any{{"text": v.Thinking, "thought": true, "thoughtSignature": v.Signature}})
		case core.ToolUseBlock:
			var args any
			_ = json.Unmarshal(v.Input, &args)
			parts = append(parts, []map[string]any{{"functionCall": map[string]any{"id": v.ID, "name": v.Name, "args": args}}})
		}
	}
	finish := map[core.StopReason]string{core.StopEndTurn: "STOP", core.StopToolUse: "STOP", core.StopMaxTokens: "MAX_TOKENS", core.StopRefusal: "SAFETY"}[r.StopReason]
	var b strings.Builder
	for i, p := range parts {
		c := map[string]any{"responseId": "resp_1", "modelVersion": model, "candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": p}}}}
		if i == len(parts)-1 {
			c["candidates"].([]any)[0].(map[string]any)["finishReason"] = finish
			c["usageMetadata"] = map[string]any{"promptTokenCount": r.Usage.InputTokens + r.Usage.CacheReadTokens,
				"cachedContentTokenCount": r.Usage.CacheReadTokens, "candidatesTokenCount": r.Usage.OutputTokens}
		}
		raw, _ := json.Marshal(c)
		fmt.Fprintf(&b, "data: %s\r\n\r\n", raw)
	}
	return b.String()
}

func scenarioServer(t *testing.T, model string, sc aitesting.Scenario, capture func(*http.Request, []byte)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if capture != nil {
			capture(r, body)
		}
		if strings.HasSuffix(r.URL.Path, ":countTokens") {
			fmt.Fprint(w, `{"totalTokens":42}`)
			return
		}
		switch {
		case sc.Err != nil:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(sc.Err.Status)
			details := "[]"
			if sc.Err.RetryAfter > 0 {
				details = fmt.Sprintf(`[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"%s"}]`, sc.Err.RetryAfter)
			}
			fmt.Fprintf(w, `{"error":{"code":%d,"message":%q,"status":"X","details":%s}}`, sc.Err.Status, sc.Err.Message, details)
		case sc.Stall:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, `data: {"candidates":[{"content":{"parts":[{"text":"wait"}]}}]}`+"\r\n\r\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			for _, c := range strings.SplitAfter(chunks(model, sc.Reply), "\r\n\r\n") {
				if c == "" {
					continue
				}
				if _, err := fmt.Fprint(w, c); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type harness struct{ model string }

func (h harness) Model() string          { return h.model }
func (h harness) ForcedToolChoice() bool { return true }
func (h harness) Driver(t *testing.T, sc aitesting.Scenario) llm.ProviderDriver {
	return gemini.New(gemini.Config{APIKey: "g-test", BaseURL: scenarioServer(t, h.model, sc, nil).URL, MaxRetries: -1})
}

func TestDriverContract(t *testing.T) {
	for _, m := range []string{"gemini-2.5-flash", "gemini-2.0-flash"} {
		t.Run(m, func(t *testing.T) { aitesting.RunDriverContract(t, harness{model: m}) })
	}
}

func capture(t *testing.T, r llm.ChatRequest) (map[string]any, *http.Request) {
	t.Helper()
	var got map[string]any
	var req *http.Request
	srv := scenarioServer(t, r.Model, aitesting.Scenario{Reply: &core.ChatResponse{Message: core.AssistantText("ok"), StopReason: core.StopEndTurn}},
		func(q *http.Request, b []byte) { _ = json.Unmarshal(b, &got); req = q })
	if _, err := gemini.New(gemini.Config{APIKey: "g-test", BaseURL: srv.URL, MaxRetries: -1}).Chat(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return got, req
}

func TestRequestBody(t *testing.T) {
	msgs := []core.Message{
		core.UserText("find it"),
		{Role: core.RoleAssistant, Content: core.Content{
			core.ThinkingBlock{Thinking: "look in a", Signature: "S1"},
			core.ThinkingBlock{Signature: "S2"}, // belongs to the call after it
			core.ToolUseBlock{ID: "c1", Name: "fs_read", Input: json.RawMessage(`{"path":"a"}`)},
		}},
		{Role: core.RoleUser, Content: core.Content{core.ToolResultBlock{ToolUseID: "c1", Content: core.Text("nope"), IsError: true}}},
	}
	body, req := capture(t, llm.ChatRequest{Model: "gemini-2.5-flash", Messages: msgs, MaxTokens: 300, Thinking: llm.ThinkingOff,
		System: []llm.SystemBlock{{Text: "be brief"}},
		Tools:  []core.ToolDef{{Name: "fs_read", InputSchema: map[string]any{"type": "object", "additionalProperties": false}}}, ToolChoice: llm.ToolChoice{Mode: llm.ToolOne, Name: "fs_read"}})

	if req.Header.Get("x-goog-api-key") != "g-test" || req.URL.Path != "/v1beta/models/gemini-2.5-flash:streamGenerateContent" || req.URL.Query().Get("alt") != "sse" {
		t.Errorf("request %s %v", req.URL, req.Header)
	}
	c, _ := json.Marshal(body["contents"])
	want := `[{"parts":[{"text":"find it"}],"role":"user"},` +
		`{"parts":[{"text":"look in a","thought":true,"thoughtSignature":"S1"},{"functionCall":{"args":{"path":"a"},"id":"c1","name":"fs_read"},"thoughtSignature":"S2"}],"role":"model"},` +
		`{"parts":[{"functionResponse":{"id":"c1","name":"fs_read","response":{"error":"nope"}}}],"role":"user"}]`
	if string(c) != want {
		t.Errorf("contents =\n %s\nwant\n %s", c, want)
	}
	tc, _ := json.Marshal(body["toolConfig"])
	if string(tc) != `{"functionCallingConfig":{"allowedFunctionNames":["fs_read"],"mode":"ANY"}}` {
		t.Errorf("toolConfig = %s", tc)
	}
	gen, _ := json.Marshal(body["generationConfig"])
	if string(gen) != `{"maxOutputTokens":300,"thinkingConfig":{"includeThoughts":false,"thinkingBudget":0}}` {
		t.Errorf("generationConfig = %s", gen)
	}
	tools, _ := json.Marshal(body["tools"])
	if !strings.Contains(string(tools), `"parametersJsonSchema":{"additionalProperties":false,"type":"object"}`) {
		t.Errorf("tools = %s", tools)
	}
}

func TestSignatureOnACallComesBackBeforeIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":{"a":1}},"thoughtSignature":"SIG"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"thoughtsTokenCount":4}}`+"\n\n")
	}))
	defer srv.Close()
	r, err := gemini.New(gemini.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1}).Chat(context.Background(), llm.ChatRequest{Model: "gemini-2.5-pro", Messages: []core.Message{core.UserText("x")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Message.Content) != 2 {
		t.Fatalf("content = %+v", r.Message.Content)
	}
	if th, ok := r.Message.Content[0].(core.ThinkingBlock); !ok || th.Signature != "SIG" || th.Thinking != "" {
		t.Errorf("first block = %+v", r.Message.Content[0])
	}
	tu := r.Message.Content.ToolUses()
	if len(tu) != 1 || tu[0].ID != "call_1" || r.StopReason != core.StopToolUse || r.Usage.OutputTokens != 6 {
		t.Errorf("tool %+v stop %s usage %+v", tu, r.StopReason, r.Usage)
	}
}

func TestCountTokens(t *testing.T) {
	var path string
	srv := scenarioServer(t, "gemini-2.5-pro", aitesting.Scenario{}, func(q *http.Request, _ []byte) { path = q.URL.Path })
	n, err := gemini.New(gemini.Config{APIKey: "k", BaseURL: srv.URL}).CountTokens(context.Background(), llm.ChatRequest{Model: "gemini-2.5-pro", Messages: []core.Message{core.UserText("x")}})
	if err != nil || n != 42 || path != "/v1beta/models/gemini-2.5-pro:countTokens" {
		t.Fatalf("n %d err %v path %s", n, err, path)
	}
}

package deepseek_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	"github.com/thescaffold/gox-packages/libs/ai/llm/deepseek"
	aitesting "github.com/thescaffold/gox-packages/libs/ai/testing"
)

// chunks renders a response as Chat Completions stream chunks.
func chunks(model string, r *core.ChatResponse) string {
	var b strings.Builder
	send := func(delta map[string]any, finish any) {
		raw, _ := json.Marshal(map[string]any{"id": "c1", "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(&b, "data: %s\n\n", raw)
	}
	send(map[string]any{"role": "assistant", "content": ""}, nil)
	tool := 0
	for _, blk := range r.Message.Content {
		switch v := blk.(type) {
		case core.TextBlock:
			h := len(v.Text) / 2
			send(map[string]any{"content": v.Text[:h]}, nil)
			send(map[string]any{"content": v.Text[h:]}, nil)
		case core.ThinkingBlock:
			send(map[string]any{"reasoning_content": v.Thinking}, nil)
		case core.ToolUseBlock:
			s := string(v.Input)
			h := len(s) / 2
			send(map[string]any{"tool_calls": []any{map[string]any{"index": tool, "id": v.ID, "type": "function", "function": map[string]any{"name": v.Name, "arguments": s[:h]}}}}, nil)
			send(map[string]any{"tool_calls": []any{map[string]any{"index": tool, "function": map[string]any{"arguments": s[h:]}}}}, nil)
			tool++
		}
	}
	finish := map[core.StopReason]string{core.StopEndTurn: "stop", core.StopToolUse: "tool_calls", core.StopMaxTokens: "length", core.StopRefusal: "content_filter"}[r.StopReason]
	send(map[string]any{}, finish)
	raw, _ := json.Marshal(map[string]any{"id": "c1", "model": model, "choices": []any{}, "usage": map[string]any{
		"prompt_tokens": r.Usage.InputTokens + r.Usage.CacheReadTokens, "completion_tokens": r.Usage.OutputTokens,
		"prompt_cache_hit_tokens": r.Usage.CacheReadTokens, "prompt_cache_miss_tokens": r.Usage.InputTokens}})
	fmt.Fprintf(&b, "data: %s\n\ndata: [DONE]\n\n", raw)
	return b.String()
}

func scenarioServer(t *testing.T, model string, sc aitesting.Scenario, capture func(*http.Request, []byte)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if capture != nil {
			capture(r, body)
		}
		switch {
		case sc.Err != nil:
			if sc.Err.RetryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(sc.Err.RetryAfter.Seconds())))
			}
			w.WriteHeader(sc.Err.Status)
			fmt.Fprintf(w, `{"error":{"message":%q,"type":"x"}}`, sc.Err.Message)
		case sc.Stall:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, `data: {"id":"c","model":"`+model+`","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`+"\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			for _, c := range strings.SplitAfter(chunks(model, sc.Reply), "\n\n") {
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

type harness struct{}

func (harness) Model() string          { return "deepseek-chat" }
func (harness) ForcedToolChoice() bool { return true }
func (harness) UnsignedThinking() bool { return true }
func (h harness) Driver(t *testing.T, sc aitesting.Scenario) llm.ProviderDriver {
	return deepseek.New(deepseek.Config{APIKey: "ds-test", BaseURL: scenarioServer(t, h.Model(), sc, nil).URL, MaxRetries: -1})
}

func TestDriverContract(t *testing.T) { aitesting.RunDriverContract(t, harness{}) }

func TestRequestBody(t *testing.T) {
	var got map[string]any
	srv := scenarioServer(t, "deepseek-chat", aitesting.Scenario{Reply: &core.ChatResponse{Message: core.AssistantText("ok"), StopReason: core.StopEndTurn}},
		func(_ *http.Request, b []byte) { _ = json.Unmarshal(b, &got) })
	msgs := []core.Message{
		core.UserText("find it"),
		{Role: core.RoleAssistant, Content: core.Content{
			core.ThinkingBlock{Thinking: "hmm"},
			core.TextBlock{Text: "checking"},
			core.ToolUseBlock{ID: "call_1", Name: "fs_read", Input: json.RawMessage(`{"path":"a"}`)},
		}},
		{Role: core.RoleUser, Content: core.Content{core.ToolResultBlock{ToolUseID: "call_1", Content: core.Text("found")}}},
	}
	_, err := deepseek.New(deepseek.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1}).Chat(context.Background(), llm.ChatRequest{
		Model: "deepseek-chat", Messages: msgs, System: []llm.SystemBlock{{Text: "be brief"}}, MaxTokens: 100,
		Tools: []core.ToolDef{{Name: "fs_read", InputSchema: map[string]any{"type": "object"}}}, ToolChoice: llm.ToolChoice{Mode: llm.ToolOne, Name: "fs_read"}})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := json.Marshal(got["messages"])
	want := `[{"content":"be brief","role":"system"},{"content":"find it","role":"user"},` +
		`{"content":"checking","role":"assistant","tool_calls":[{"function":{"arguments":"{\"path\":\"a\"}","name":"fs_read"},"id":"call_1","type":"function"}]},` +
		`{"content":"found","role":"tool","tool_call_id":"call_1"}]`
	if string(m) != want {
		t.Errorf("messages =\n %s\nwant\n %s", m, want)
	}
	tc, _ := json.Marshal(got["tool_choice"])
	if string(tc) != `{"function":{"name":"fs_read"},"type":"function"}` || got["max_tokens"] != 100.0 {
		t.Errorf("tool_choice %s max_tokens %v", tc, got["max_tokens"])
	}
}

func TestReasonerRefusesToolsAndImages(t *testing.T) {
	d := deepseek.New(deepseek.Config{APIKey: "k", BaseURL: "http://127.0.0.1:1", MaxRetries: -1})
	_, err := d.Chat(context.Background(), llm.ChatRequest{Model: "deepseek-reasoner", Messages: []core.Message{core.UserText("x")},
		Tools: []core.ToolDef{{Name: "a", InputSchema: map[string]any{"type": "object"}}}})
	if err == nil || !strings.Contains(err.Error(), "does not support tools") {
		t.Errorf("tools on the reasoner: %v", err)
	}
	_, err = d.Chat(context.Background(), llm.ChatRequest{Model: "deepseek-chat", Messages: []core.Message{{Role: core.RoleUser, Content: core.Content{
		core.ImageBlock{Source: core.MediaSource{Kind: "url", URL: "https://x/y.png"}}}}}})
	if err == nil {
		t.Error("an image was accepted")
	}
}

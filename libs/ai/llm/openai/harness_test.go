package openai_test

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
	"github.com/thescaffold/gox-packages/libs/ai/llm/openai"
	aitesting "github.com/thescaffold/gox-packages/libs/ai/testing"
)

// sse renders a response in the Responses API's streaming wire format.
func sse(model string, r *core.ChatResponse) string {
	var b strings.Builder
	ev := func(v map[string]any) {
		raw, _ := json.Marshal(v)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", v["type"], raw)
	}
	ev(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_1", "model": model, "status": "in_progress"}})
	refusal := r.StopReason == core.StopRefusal
	for i, blk := range r.Message.Content {
		switch v := blk.(type) {
		case core.TextBlock:
			ev(map[string]any{"type": "response.output_item.added", "output_index": i, "item": map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "content": []any{}}})
			h := len(v.Text) / 2
			kind := "response.output_text.delta"
			if refusal {
				kind = "response.refusal.delta"
			}
			ev(map[string]any{"type": kind, "output_index": i, "content_index": 0, "delta": v.Text[:h]})
			ev(map[string]any{"type": kind, "output_index": i, "content_index": 0, "delta": v.Text[h:]})
			part := map[string]any{"type": "output_text", "text": v.Text}
			if refusal {
				part = map[string]any{"type": "refusal", "refusal": v.Text}
			}
			ev(map[string]any{"type": "response.output_item.done", "output_index": i, "item": map[string]any{"id": "msg_1", "type": "message", "content": []any{part}}})
		case core.ThinkingBlock:
			ev(map[string]any{"type": "response.output_item.added", "output_index": i, "item": map[string]any{"type": "reasoning", "summary": []any{}}})
			ev(map[string]any{"type": "response.reasoning_summary_text.delta", "output_index": i, "summary_index": 0, "delta": v.Thinking})
			ev(map[string]any{"type": "response.output_item.done", "output_index": i, "item": map[string]any{"type": "reasoning",
				"summary": []any{map[string]any{"type": "summary_text", "text": v.Thinking}}, "encrypted_content": v.Signature}})
		case core.ToolUseBlock:
			ev(map[string]any{"type": "response.output_item.added", "output_index": i, "item": map[string]any{"id": "fc_1", "type": "function_call", "call_id": v.ID, "name": v.Name, "arguments": ""}})
			s := string(v.Input)
			h := len(s) / 2
			ev(map[string]any{"type": "response.function_call_arguments.delta", "output_index": i, "delta": s[:h]})
			ev(map[string]any{"type": "response.function_call_arguments.delta", "output_index": i, "delta": s[h:]})
			ev(map[string]any{"type": "response.output_item.done", "output_index": i, "item": map[string]any{"id": "fc_1", "type": "function_call", "call_id": v.ID, "name": v.Name, "arguments": s}})
		}
	}
	usage := map[string]any{"input_tokens": r.Usage.InputTokens + r.Usage.CacheReadTokens, "output_tokens": r.Usage.OutputTokens,
		"input_tokens_details": map[string]any{"cached_tokens": r.Usage.CacheReadTokens}}
	final := map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_1", "model": model, "status": "completed", "usage": usage}}
	if r.StopReason == core.StopMaxTokens {
		final = map[string]any{"type": "response.incomplete", "response": map[string]any{"id": "resp_1", "model": model, "status": "incomplete",
			"incomplete_details": map[string]any{"reason": "max_output_tokens"}, "usage": usage}}
	}
	ev(final)
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
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(sc.Err.Status)
			fmt.Fprintf(w, `{"error":{"message":%q,"type":"x","code":null}}`, sc.Err.Message)
		case sc.Stall:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"model\":\""+model+"\"}}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			for _, chunk := range strings.SplitAfter(sse(model, sc.Reply), "\n\n") {
				if chunk == "" {
					continue
				}
				if _, err := fmt.Fprint(w, chunk); err != nil {
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
	srv := scenarioServer(t, h.model, sc, nil)
	return openai.New(openai.Config{APIKey: "sk-test", BaseURL: srv.URL, MaxRetries: -1})
}

func TestDriverContract(t *testing.T) {
	for _, m := range []string{"gpt-5-mini", "gpt-4.1"} {
		t.Run(m, func(t *testing.T) { aitesting.RunDriverContract(t, harness{model: m}) })
	}
}

// capture runs one call and returns the JSON body the driver sent.
func capture(t *testing.T, r llm.ChatRequest) (map[string]any, http.Header) {
	t.Helper()
	var got map[string]any
	var hdr http.Header
	srv := scenarioServer(t, r.Model, aitesting.Scenario{Reply: &core.ChatResponse{Message: core.AssistantText("ok"), StopReason: core.StopEndTurn}},
		func(req *http.Request, b []byte) { _ = json.Unmarshal(b, &got); hdr = req.Header.Clone() })
	d := openai.New(openai.Config{APIKey: "sk-test", BaseURL: srv.URL, MaxRetries: -1, Organization: "org_1"})
	if _, err := d.Chat(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return got, hdr
}

func TestRequestBody(t *testing.T) {
	msgs := []core.Message{
		core.UserText("find it"),
		{Role: core.RoleAssistant, Content: core.Content{
			core.ThinkingBlock{Thinking: "look in a", Signature: "rs_9|ENC"},
			core.TextBlock{Text: "checking"},
			core.ToolUseBlock{ID: "call_1", Name: "fs_read", Input: json.RawMessage(`{"path":"a"}`)},
		}},
		{Role: core.RoleUser, Content: core.Content{core.ToolResultBlock{ToolUseID: "call_1", Content: core.Text("nope"), IsError: true}, core.TextBlock{Text: "and now?"}}},
	}
	body, hdr := capture(t, llm.ChatRequest{Model: "gpt-5", Messages: msgs, MaxTokens: 500, Effort: llm.EffortMax,
		System: []llm.SystemBlock{{Text: "be brief"}, {Text: "be kind"}},
		Tools:  []core.ToolDef{{Name: "fs_read", Description: "read", InputSchema: map[string]any{"type": "object"}}}, ToolChoice: llm.ToolChoice{Mode: llm.ToolAny}})

	if hdr.Get("Authorization") != "Bearer sk-test" || hdr.Get("OpenAI-Organization") != "org_1" {
		t.Errorf("headers = %v", hdr)
	}
	want := map[string]any{"model": "gpt-5", "stream": true, "store": false, "instructions": "be brief\n\nbe kind", "max_output_tokens": 500.0, "tool_choice": "required"}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v, want %v", k, body[k], v)
		}
	}
	if r := body["reasoning"].(map[string]any); r["effort"] != "high" || r["summary"] != "auto" {
		t.Errorf("reasoning = %v", r)
	}
	in, _ := json.Marshal(body["input"])
	wantIn := `[{"content":[{"text":"find it","type":"input_text"}],"role":"user"},` +
		`{"encrypted_content":"ENC","id":"rs_9","summary":[{"text":"look in a","type":"summary_text"}],"type":"reasoning"},` +
		`{"content":[{"text":"checking","type":"output_text"}],"role":"assistant"},` +
		`{"arguments":"{\"path\":\"a\"}","call_id":"call_1","name":"fs_read","type":"function_call"},` +
		`{"call_id":"call_1","output":"Error: nope","type":"function_call_output"},` +
		`{"content":[{"text":"and now?","type":"input_text"}],"role":"user"}]`
	if string(in) != wantIn {
		t.Errorf("input =\n %s\nwant\n %s", in, wantIn)
	}
}

func TestNonReasoningModelSendsNoReasoning(t *testing.T) {
	body, _ := capture(t, llm.ChatRequest{Model: "gpt-4.1", Messages: []core.Message{core.UserText("hi")}, Effort: llm.EffortHigh})
	if _, ok := body["reasoning"]; ok {
		t.Errorf("reasoning sent to gpt-4.1: %v", body["reasoning"])
	}
	if _, ok := body["include"]; ok {
		t.Errorf("include sent to gpt-4.1")
	}
}

func TestReasoningSignatureKeepsTheItemID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"model\":\"gpt-5\"}}\n\n"+
			"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"rs_7\",\"type\":\"reasoning\"}}\n\n"+
			"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"rs_7\",\"type\":\"reasoning\",\"summary\":[{\"text\":\"a\"},{\"text\":\"b\"}],\"encrypted_content\":\"E\"}}\n\n"+
			"data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"message\"}}\n\n"+
			"data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hi\"}]}}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n")
	}))
	defer srv.Close()
	r, err := openai.New(openai.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1}).Chat(context.Background(), llm.ChatRequest{Model: "gpt-5", Messages: []core.Message{core.UserText("x")}})
	if err != nil {
		t.Fatal(err)
	}
	th := r.Message.Content[0].(core.ThinkingBlock)
	if th.Thinking != "a\n\nb" || th.Signature != "rs_7|E" {
		t.Errorf("thinking = %+v", th)
	}
	if r.Message.Content.PlainText() != "hi" || r.Usage.InputTokens != 3 {
		t.Errorf("text %q usage %+v", r.Message.Content.PlainText(), r.Usage)
	}
}

func TestRetriesBeforeOutput(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After-Ms", "10")
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse("gpt-5", &core.ChatResponse{Message: core.AssistantText("ok"), StopReason: core.StopEndTurn}))
	}))
	defer srv.Close()
	r, err := openai.New(openai.Config{APIKey: "k", BaseURL: srv.URL}).Chat(context.Background(), llm.ChatRequest{Model: "gpt-5", Messages: []core.Message{core.UserText("x")}})
	if err != nil || r.Message.Content.PlainText() != "ok" || calls != 2 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

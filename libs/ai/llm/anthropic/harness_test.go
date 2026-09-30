package anthropic_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	"github.com/thescaffold/gox-packages/libs/ai/llm/anthropic"
	aitesting "github.com/thescaffold/gox-packages/libs/ai/testing"
)

// sse renders a response in the Messages API's streaming wire format.
func sse(model string, r *core.ChatResponse) string {
	var b strings.Builder
	ev := func(name string, v any) {
		raw, _ := json.Marshal(v)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", name, raw)
	}
	ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_01", "type": "message", "role": "assistant", "model": model, "content": []any{},
		"usage": map[string]any{"input_tokens": r.Usage.InputTokens, "output_tokens": 1,
			"cache_read_input_tokens": r.Usage.CacheReadTokens, "cache_creation_input_tokens": r.Usage.CacheWriteTokens}}})
	fmt.Fprint(&b, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
	for i, blk := range r.Message.Content {
		start := map[string]any{}
		var deltas []map[string]any
		switch v := blk.(type) {
		case core.TextBlock:
			start = map[string]any{"type": "text", "text": ""}
			h := len(v.Text) / 2
			deltas = []map[string]any{{"type": "text_delta", "text": v.Text[:h]}, {"type": "text_delta", "text": v.Text[h:]}}
		case core.ThinkingBlock:
			start = map[string]any{"type": "thinking", "thinking": ""}
			deltas = []map[string]any{{"type": "thinking_delta", "thinking": v.Thinking}, {"type": "signature_delta", "signature": v.Signature}}
		case core.ToolUseBlock:
			start = map[string]any{"type": "tool_use", "id": v.ID, "name": v.Name, "input": map[string]any{}}
			s := string(v.Input)
			h := len(s) / 2
			deltas = []map[string]any{{"type": "input_json_delta", "partial_json": s[:h]}, {"type": "input_json_delta", "partial_json": s[h:]}}
		}
		ev("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": start})
		for _, d := range deltas {
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": d})
		}
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": string(r.StopReason)},
		"usage": map[string]any{"output_tokens": r.Usage.OutputTokens}})
	ev("message_stop", map[string]any{"type": "message_stop"})
	return b.String()
}

func statusFor(e *core.ProviderError) int { return e.Status }

// scenarioServer serves one scenario over HTTP exactly as the API would.
func scenarioServer(t *testing.T, model string, sc aitesting.Scenario, capture func(*http.Request, []byte)) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 0, 1024)
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				break
			}
		}
		if capture != nil {
			capture(r, body)
		}
		if strings.HasSuffix(r.URL.Path, "/count_tokens") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"input_tokens":42}`)
			return
		}
		switch {
		case sc.Err != nil:
			if sc.Err.RetryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(sc.Err.RetryAfter.Seconds())))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusFor(sc.Err))
			fmt.Fprintf(w, `{"type":"error","error":{"type":"x","message":%q}}`, sc.Err.Message)
		case sc.Stall:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\""+model+"\",\"usage\":{\"input_tokens\":1}}}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			full := sse(model, sc.Reply)
			// flush event by event so a consumer sees a real stream
			for _, chunk := range strings.SplitAfter(full, "\n\n") {
				if chunk == "" {
					continue
				}
				if _, err := fmt.Fprint(w, chunk); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

type harness struct{ model string }

func (h harness) Model() string          { return h.model }
func (h harness) ForcedToolChoice() bool { return !strings.HasPrefix(h.model, "claude-fable") }

func (h harness) Driver(t *testing.T, sc aitesting.Scenario) llm.ProviderDriver {
	srv := scenarioServer(t, h.model, sc, nil)
	return anthropic.New(anthropic.Config{APIKey: "sk-test", BaseURL: srv.URL, MaxRetries: -1})
}

func TestDriverContract(t *testing.T) {
	for _, m := range []string{"claude-sonnet-5", "claude-fable-5-1"} {
		t.Run(m, func(t *testing.T) { aitesting.RunDriverContract(t, harness{model: m}) })
	}
}

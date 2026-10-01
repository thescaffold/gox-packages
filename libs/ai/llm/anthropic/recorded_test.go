package anthropic_test

// Recorded fixtures (PLAN M1-27).
//
// The other tests in this package build their responses from the documented
// event format. These are the real thing: TestRecordFixtures makes a fixed set
// of calls against the live API and saves each request and response under
// testdata/recorded, and TestRecordedFixturesReplay plays them back offline.
//
// The replay checks two things the hand-written fixtures cannot: that the
// driver still sends byte-for-byte (JSON-equal) the request the live API
// accepted when it was recorded, and that it still reads what the live API
// actually sent.
//
// To (re)record, with a real key (a few cents; haiku-4-5 and sonnet-5):
//
//	ANTHROPIC_API_KEY=... ANTHROPIC_RECORD=1 go test ./llm/anthropic -run TestRecordFixtures -v
//
// Fixtures hold request and response bodies only; the key is sent in a header
// and is never written. Review the diff before committing a re-recording.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	"github.com/thescaffold/gox-packages/libs/ai/llm/anthropic"
)

// recordedDir holds the fixtures (overridable for the harness's own tests).
var recordedDir = "testdata/recorded"

// fixture is one recorded exchange.
type fixture struct {
	Name        string          `json:"name"`
	Model       string          `json:"model"`
	Path        string          `json:"path"`
	Request     json.RawMessage `json:"request"`
	Status      int             `json:"status"`
	ContentType string          `json:"contentType"`
	RetryAfter  string          `json:"retryAfter,omitempty"`
	Response    string          `json:"response"`
	RecordedAt  string          `json:"recordedAt"`
}

// recordedCall is one scripted call and what must hold of its outcome, live or
// replayed.
type recordedCall struct {
	name  string
	model string
	run   func(ctx context.Context, d llm.ProviderDriver) (*core.ChatResponse, error)
	check func(t *testing.T, r *core.ChatResponse, err error)
}

func chatReq(model string, mut func(*llm.ChatRequest)) llm.ChatRequest {
	r := llm.ChatRequest{Model: model, MaxTokens: 256}
	mut(&r)
	return r
}

func chatCall(req llm.ChatRequest) func(context.Context, llm.ProviderDriver) (*core.ChatResponse, error) {
	return func(ctx context.Context, d llm.ProviderDriver) (*core.ChatResponse, error) { return d.Chat(ctx, req) }
}

func streamCall(req llm.ChatRequest) func(context.Context, llm.ProviderDriver) (*core.ChatResponse, error) {
	return func(ctx context.Context, d llm.ProviderDriver) (*core.ChatResponse, error) {
		ch, err := d.Stream(ctx, req)
		if err != nil {
			return nil, err
		}
		return llm.Collect(ctx, ch)
	}
}

func mustOK(t *testing.T, r *core.ChatResponse, err error) *core.ChatResponse {
	t.Helper()
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if r == nil {
		t.Fatal("no response")
	}
	if r.Usage.InputTokens == 0 || r.Usage.OutputTokens == 0 {
		t.Fatalf("usage not read: %+v", r.Usage)
	}
	return r
}

func toolUse(t *testing.T, r *core.ChatResponse) core.ToolUseBlock {
	t.Helper()
	for _, b := range r.Message.Content {
		if tu, ok := b.(core.ToolUseBlock); ok {
			return tu
		}
	}
	t.Fatalf("no tool call in %+v", r.Message.Content)
	return core.ToolUseBlock{}
}

const haiku, sonnet = "claude-haiku-4-5", "claude-sonnet-5"

var weatherTool = core.ToolDef{
	Name: "get_weather", Description: "Get the current weather for a city.", Strict: true,
	InputSchema: map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"city"},
		"properties": map[string]any{"city": map[string]any{"type": "string"}},
	},
}

var writeFileTool = core.ToolDef{
	Name: "write_file", Description: "Write a file.", EagerInputStreaming: true,
	InputSchema: map[string]any{
		"type": "object", "required": []string{"path", "content"},
		"properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}},
	},
}

func recordedCalls() []recordedCall {
	ping := core.UserText("Reply with the single word: pong")
	return []recordedCall{
		{name: "chat_text", model: haiku,
			run: chatCall(chatReq(haiku, func(r *llm.ChatRequest) { r.Messages = []core.Message{ping} })),
			check: func(t *testing.T, r *core.ChatResponse, err error) {
				r = mustOK(t, r, err)
				if !strings.Contains(strings.ToLower(r.Message.Content.PlainText()), "pong") || r.StopReason != core.StopEndTurn {
					t.Fatalf("text %q stop %q", r.Message.Content.PlainText(), r.StopReason)
				}
			}},
		{name: "stream_text", model: haiku,
			run: streamCall(chatReq(haiku, func(r *llm.ChatRequest) {
				r.System = []llm.SystemBlock{{Text: "You are terse."}}
				r.Messages = []core.Message{core.UserText("Count from one to five, separated by commas.")}
			})),
			check: func(t *testing.T, r *core.ChatResponse, err error) {
				r = mustOK(t, r, err)
				if !strings.Contains(r.Message.Content.PlainText(), "3") || r.StopReason != core.StopEndTurn {
					t.Fatalf("text %q stop %q", r.Message.Content.PlainText(), r.StopReason)
				}
			}},
		{name: "tool_forced_strict", model: haiku,
			run: chatCall(chatReq(haiku, func(r *llm.ChatRequest) {
				r.Messages = []core.Message{core.UserText("What is the weather in Lagos?")}
				r.Tools = []core.ToolDef{weatherTool}
				r.ToolChoice = llm.ToolChoice{Mode: llm.ToolOne, Name: "get_weather"}
			})),
			check: func(t *testing.T, r *core.ChatResponse, err error) {
				r = mustOK(t, r, err)
				tu := toolUse(t, r)
				var in struct{ City string }
				if err := json.Unmarshal(tu.Input, &in); err != nil || !strings.EqualFold(in.City, "Lagos") || r.StopReason != core.StopToolUse {
					t.Fatalf("tool call %s %s (stop %q): %v", tu.Name, tu.Input, r.StopReason, err)
				}
			}},
		{name: "tool_eager_input", model: haiku,
			run: streamCall(chatReq(haiku, func(r *llm.ChatRequest) {
				r.MaxTokens = 512
				r.Messages = []core.Message{core.UserText("Write a file hello.txt containing the text: hello world")}
				r.Tools = []core.ToolDef{writeFileTool}
				r.ToolChoice = llm.ToolChoice{Mode: llm.ToolOne, Name: "write_file"}
			})),
			check: func(t *testing.T, r *core.ChatResponse, err error) {
				r = mustOK(t, r, err)
				tu := toolUse(t, r)
				var in struct{ Path, Content string }
				if err := json.Unmarshal(tu.Input, &in); err != nil || in.Path == "" || !strings.Contains(in.Content, "hello") {
					t.Fatalf("eagerly streamed input %s did not assemble into a valid call: %v", tu.Input, err)
				}
			}},
		{name: "thinking_adaptive", model: sonnet,
			run: streamCall(chatReq(sonnet, func(r *llm.ChatRequest) {
				r.MaxTokens = 2048
				r.Effort = llm.EffortLow
				r.Messages = []core.Message{core.UserText("What is 17 times 23? Answer with just the number.")}
			})),
			check: func(t *testing.T, r *core.ChatResponse, err error) {
				r = mustOK(t, r, err)
				if !strings.Contains(r.Message.Content.PlainText(), "391") {
					t.Fatalf("answer %q", r.Message.Content.PlainText())
				}
			}},
		{name: "max_tokens", model: haiku,
			run: chatCall(chatReq(haiku, func(r *llm.ChatRequest) {
				r.MaxTokens = 5
				r.Messages = []core.Message{core.UserText("Write a long essay about the history of the bicycle.")}
			})),
			check: func(t *testing.T, r *core.ChatResponse, err error) {
				r = mustOK(t, r, err)
				if r.StopReason != core.StopMaxTokens {
					t.Fatalf("stop %q, want max_tokens", r.StopReason)
				}
			}},
		{name: "error_unknown_model", model: "claude-haiku-4-5-not-a-real-model",
			run: chatCall(chatReq("claude-haiku-4-5-not-a-real-model", func(r *llm.ChatRequest) { r.Messages = []core.Message{ping} })),
			check: func(t *testing.T, r *core.ChatResponse, err error) {
				var pe *core.ProviderError
				if !errors.As(err, &pe) || pe.Kind != core.KindNotFound || pe.Status != 404 || pe.Retryable() {
					t.Fatalf("err = %v, want a non-retryable not-found ProviderError", err)
				}
			}},
		{name: "count_tokens", model: haiku,
			run: func(ctx context.Context, d llm.ProviderDriver) (*core.ChatResponse, error) {
				n, err := d.CountTokens(ctx, chatReq(haiku, func(r *llm.ChatRequest) { r.Messages = []core.Message{ping} }))
				if err != nil {
					return nil, err
				}
				return &core.ChatResponse{Usage: core.Usage{InputTokens: n, OutputTokens: 1}}, nil
			},
			check: func(t *testing.T, r *core.ChatResponse, err error) {
				if r = mustOK(t, r, err); r.Usage.InputTokens < 5 {
					t.Fatalf("counted %d tokens for a whole message", r.Usage.InputTokens)
				}
			}},
	}
}

// ── recording ────────────────────────────────────────────────────────────────

// recorder is a transport that forwards to the live API and remembers the
// exchange of the call in progress.
type recorder struct {
	mu   sync.Mutex
	call recordedCall
	got  []fixture
	next http.RoundTripper
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	resp, err := r.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	raw, rerr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	r.mu.Lock()
	r.got = append(r.got, fixture{
		Name: r.call.name, Model: r.call.model, Path: req.URL.Path, Request: body, Status: resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"), RetryAfter: resp.Header.Get("Retry-After"),
		Response: string(raw), RecordedAt: time.Now().UTC().Format(time.RFC3339),
	})
	r.mu.Unlock()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return resp, rerr
}

func TestRecordFixtures(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" || os.Getenv("ANTHROPIC_RECORD") != "1" {
		t.Skip("set ANTHROPIC_API_KEY and ANTHROPIC_RECORD=1 to re-record the fixtures")
	}
	if err := os.MkdirAll(recordedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range recordedCalls() {
		t.Run(c.name, func(t *testing.T) {
			rec := &recorder{call: c, next: http.DefaultTransport}
			d := anthropic.New(anthropic.Config{APIKey: key, HTTPClient: &http.Client{Transport: rec}, MaxRetries: -1})
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			r, err := c.run(ctx, d)
			c.check(t, r, err) // never save an exchange the live API did not satisfy
			if t.Failed() {
				return
			}
			if len(rec.got) != 1 {
				t.Fatalf("recorded %d exchanges, want exactly 1", len(rec.got))
			}
			raw, err := json.MarshalIndent(rec.got[0], "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte(key)) {
				t.Fatal("the key appears in the recording; refusing to write it")
			}
			if err := os.WriteFile(filepath.Join(recordedDir, c.name+".json"), append(raw, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// ── replay ───────────────────────────────────────────────────────────────────

func loadFixture(t *testing.T, name string) (fixture, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(recordedDir, name+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return fixture{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return f, true
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("not JSON: %s", a)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("not JSON: %s", b)
	}
	return reflect.DeepEqual(x, y)
}

func recordedFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(recordedDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(names)
	return names
}

func TestRecordedFixturesReplay(t *testing.T) {
	files := recordedFiles(t)
	if len(files) == 0 {
		t.Skip("no recorded fixtures yet: record them with ANTHROPIC_API_KEY=... ANTHROPIC_RECORD=1 go test ./llm/anthropic -run TestRecordFixtures")
	}
	scripted := map[string]bool{}
	for _, c := range recordedCalls() {
		scripted[c.name] = true
		t.Run(c.name, func(t *testing.T) {
			f, ok := loadFixture(t, c.name)
			if !ok {
				t.Fatalf("no recording for %q: re-record (the scripted calls changed)", c.name)
			}
			sent, r, err := replay(t, c, f)
			if !sameJSON(t, sent, f.Request) {
				t.Errorf("the request the driver sends changed since it was recorded.\nsent:     %s\nrecorded: %s", sent, f.Request)
			}
			c.check(t, r, err)
		})
	}
	for _, name := range files {
		if !scripted[name] {
			t.Errorf("fixture %q belongs to no scripted call: delete it or script it", name)
		}
	}
}

// replay serves one recorded response to the driver and returns the request the
// driver sent along with what it made of the response.
func replay(t *testing.T, c recordedCall, f fixture) (sent []byte, r *core.ChatResponse, err error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sent, _ = io.ReadAll(req.Body)
		if req.URL.Path != f.Path {
			t.Errorf("driver called %s, the recording called %s", req.URL.Path, f.Path)
		}
		if f.ContentType != "" {
			w.Header().Set("Content-Type", f.ContentType)
		}
		if f.RetryAfter != "" {
			w.Header().Set("Retry-After", f.RetryAfter)
		}
		w.WriteHeader(f.Status)
		for _, chunk := range strings.SplitAfter(f.Response, "\n\n") {
			if chunk == "" {
				continue
			}
			_, _ = w.Write([]byte(chunk))
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()
	d := anthropic.New(anthropic.Config{APIKey: "sk-replay", BaseURL: srv.URL, MaxRetries: -1})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err = c.run(ctx, d)
	return sent, r, err
}

func TestRecordedFixturesCarryNoSecrets(t *testing.T) {
	for _, name := range recordedFiles(t) {
		raw, err := os.ReadFile(filepath.Join(recordedDir, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"sk-ant", "x-api-key", "authorization", "Bearer "} {
			if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(bad)) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
	}
}

// ── the harness's own tests ──────────────────────────────────────────────────
//
// These use a synthetic exchange (never saved as a recording) to prove the
// replay notices what it exists to notice.

func syntheticFixture(t *testing.T, c recordedCall) fixture {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse(c.model, &core.ChatResponse{
			Message:    core.Message{Role: core.RoleAssistant, Content: core.Content{core.TextBlock{Text: "pong"}}},
			StopReason: core.StopEndTurn, Usage: core.Usage{InputTokens: 9, OutputTokens: 2},
		})))
	}))
	defer srv.Close()
	d := anthropic.New(anthropic.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})
	if _, err := c.run(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return fixture{Name: c.name, Model: c.model, Path: "/v1/messages", Request: body, Status: 200, ContentType: "text/event-stream",
		Response: sse(c.model, &core.ChatResponse{
			Message:    core.Message{Role: core.RoleAssistant, Content: core.Content{core.TextBlock{Text: "pong"}}},
			StopReason: core.StopEndTurn, Usage: core.Usage{InputTokens: 9, OutputTokens: 2},
		})}
}

func TestReplayHarness_PassesWhenNothingDrifted(t *testing.T) {
	c := recordedCalls()[0]
	f := syntheticFixture(t, c)
	sent, r, err := replay(t, c, f)
	if !sameJSON(t, sent, f.Request) {
		t.Fatalf("an unchanged driver was reported as drifted:\n%s\n%s", sent, f.Request)
	}
	c.check(t, r, err)
}

func TestReplayHarness_NoticesADriftedRequest(t *testing.T) {
	c := recordedCalls()[0]
	f := syntheticFixture(t, c)
	f.Request = json.RawMessage(strings.Replace(string(f.Request), `"max_tokens":256`, `"max_tokens":257`, 1))
	if string(f.Request) == "" || !strings.Contains(string(f.Request), "257") {
		t.Fatalf("the test did not change the request: %s", f.Request)
	}
	sent, _, _ := replay(t, c, f)
	if sameJSON(t, sent, f.Request) {
		t.Fatal("a changed request went unnoticed")
	}
}

func TestReplayHarness_RecordedErrorsComeBackTyped(t *testing.T) {
	var c recordedCall
	for _, x := range recordedCalls() {
		if x.name == "error_unknown_model" {
			c = x
		}
	}
	f := fixture{Path: "/v1/messages", Status: 404, ContentType: "application/json",
		Response: `{"type":"error","error":{"type":"not_found_error","message":"model: x"}}`}
	_, r, err := replay(t, c, f)
	c.check(t, r, err)
}

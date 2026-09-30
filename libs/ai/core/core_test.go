package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
)

func allBlocks() core.Content {
	return core.Content{
		core.TextBlock{Text: "hi", Cache: &core.CacheHint{TTL: "1h"}},
		core.ImageBlock{Source: core.MediaSource{Kind: "base64", MediaType: "image/png", Data: "AAAA"}},
		core.DocumentBlock{Source: core.MediaSource{Kind: "url", MediaType: "application/pdf", URL: "https://x/y.pdf"}, Title: "spec"},
		core.ToolUseBlock{ID: "tu_1", Name: "fs.read", Input: json.RawMessage(`{"path":"a.txt"}`)},
		core.ToolResultBlock{ToolUseID: "tu_1", IsError: true, Content: core.Content{core.TextBlock{Text: "nope"}}},
		core.ThinkingBlock{Thinking: "hmm", Signature: "sig=="},
		core.RedactedThinkingBlock{Data: "opaque"},
	}
}

func TestContentRoundTripsEveryBlockType(t *testing.T) {
	m := core.Message{Role: core.RoleAssistant, Content: allBlocks(), Name: "x"}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back core.Message
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, back) {
		t.Fatalf("round trip changed the message:\n got %#v\nwant %#v", back, m)
	}
	// every block carries its discriminator on the wire
	for _, want := range []string{`"type":"text"`, `"type":"image"`, `"type":"document"`, `"type":"tool_use"`, `"type":"tool_result"`, `"type":"thinking"`, `"type":"redacted_thinking"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("wire form missing %s: %s", want, raw)
		}
	}
}

func TestThinkingSignatureIsPreservedByteForByte(t *testing.T) {
	sig := "EqQBCkYIBxgCKkD+/=="
	raw, _ := json.Marshal(core.Content{core.ThinkingBlock{Thinking: "t", Signature: sig}})
	var back core.Content
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back[0].(core.ThinkingBlock).Signature != sig {
		t.Fatal("signature altered")
	}
}

func TestNilContentMarshalsAsEmptyArray(t *testing.T) {
	raw, err := json.Marshal(core.Message{Role: core.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"content":[]`) {
		t.Fatalf("want empty array, got %s", raw)
	}
}

func TestUnmarshalRejectsUnknownOrMissingTypeRatherThanDropping(t *testing.T) {
	for name, in := range map[string]string{
		"unknown": `[{"type":"hologram","x":1}]`,
		"missing": `[{"text":"hi"}]`,
		"badbody": `[{"type":"tool_use","id":5}]`,
		"notlist": `{"type":"text"}`,
	} {
		var c core.Content
		if err := json.Unmarshal([]byte(in), &c); err == nil {
			t.Errorf("%s: accepted %s -> %#v", name, in, c)
		}
	}
}

func TestMarshalRejectsNilBlock(t *testing.T) {
	if _, err := json.Marshal(core.Content{nil}); err == nil {
		t.Fatal("nil block marshalled")
	}
}

func TestPlainTextAndToolUses(t *testing.T) {
	c := core.Content{
		core.TextBlock{Text: "a"},
		core.ToolUseBlock{ID: "1", Name: "x"},
		core.ThinkingBlock{Thinking: "ignored"},
		core.TextBlock{Text: "b"},
		core.ToolUseBlock{ID: "2", Name: "y"},
	}
	if got := c.PlainText(); got != "ab" {
		t.Fatalf("PlainText = %q", got)
	}
	tu := c.ToolUses()
	if len(tu) != 2 || tu[0].ID != "1" || tu[1].ID != "2" {
		t.Fatalf("ToolUses = %#v", tu)
	}
}

func TestMessageValidate(t *testing.T) {
	ok := core.UserText("hi")
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]core.Message{
		"role":          {Role: "robot", Content: core.Text("x")},
		"empty":         {Role: core.RoleUser},
		"nil block":     {Role: core.RoleUser, Content: core.Content{nil}},
		"tool_use id":   {Role: core.RoleAssistant, Content: core.Content{core.ToolUseBlock{Name: "x"}}},
		"tool_use name": {Role: core.RoleAssistant, Content: core.Content{core.ToolUseBlock{ID: "1"}}},
		"tool_result":   {Role: core.RoleUser, Content: core.Content{core.ToolResultBlock{}}},
	}
	for n, m := range bad {
		if m.Validate() == nil {
			t.Errorf("%s: accepted", n)
		}
	}
}

func TestUsageAddAndHitRate(t *testing.T) {
	a := core.Usage{InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4}
	b := core.Usage{InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40}
	if got, want := a.Add(b), (core.Usage{InputTokens: 11, OutputTokens: 22, CacheReadTokens: 33, CacheWriteTokens: 44}); got != want {
		t.Fatalf("Add = %+v", got)
	}
	if (core.Usage{}).CacheHitRate() != 0 {
		t.Fatal("empty usage must have rate 0, not NaN")
	}
	u := core.Usage{InputTokens: 100, CacheReadTokens: 600, CacheWriteTokens: 300}
	if r := u.CacheHitRate(); math.Abs(r-0.6) > 1e-9 {
		t.Fatalf("rate = %v, want 0.6", r)
	}
}

func TestCostIsExactAndRoundsUp(t *testing.T) {
	sonnet := core.Pricing{Model: "claude-sonnet-5", InputPerMTok: 2_000_000, OutputPerMTok: 10_000_000, CacheReadPerMTok: 200_000, CacheWritePerMTok: 2_500_000}
	// 1M in + 1M out = $2 + $10
	if got := core.Cost(core.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}, sonnet); got != 12_000_000 {
		t.Fatalf("cost = %d", got)
	}
	// $2/MTok is exactly 2 micro-USD per token
	if got := core.Cost(core.Usage{InputTokens: 7}, sonnet); got != 14 {
		t.Fatalf("7 tokens = %d", got)
	}
	// cache components are priced
	if got := core.Cost(core.Usage{CacheReadTokens: 5_000_000, CacheWriteTokens: 2_000_000}, sonnet); got != 1_000_000+5_000_000 {
		t.Fatalf("cache cost = %d", got)
	}
	// a fractional micro-dollar rounds UP, once over the sum
	cheap := core.Pricing{InputPerMTok: 100_000} // 0.1 micro-USD per token
	if got := core.Cost(core.Usage{InputTokens: 1}, cheap); got != 1 {
		t.Fatalf("1 cheap token = %d, want 1 (never zero for real usage)", got)
	}
	if got := core.Cost(core.Usage{InputTokens: 10}, cheap); got != 1 {
		t.Fatalf("10 cheap tokens = %d, want exactly 1", got)
	}
	// rounding is over the sum, not per component: 5+5 tenths = 1, not 2
	both := core.Pricing{InputPerMTok: 100_000, OutputPerMTok: 100_000}
	if got := core.Cost(core.Usage{InputTokens: 5, OutputTokens: 5}, both); got != 1 {
		t.Fatalf("rounded per component: %d", got)
	}
	if got := core.Cost(core.Usage{}, sonnet); got != 0 {
		t.Fatalf("no usage costs %d", got)
	}
}

func TestCostSaturatesOnOverflowAndIgnoresNegatives(t *testing.T) {
	huge := core.Pricing{InputPerMTok: math.MaxInt64}
	if got := core.Cost(core.Usage{InputTokens: math.MaxInt64}, huge); got != core.MicroUSD(math.MaxInt64) {
		t.Fatalf("overflow = %d", got)
	}
	if got := core.Cost(core.Usage{InputTokens: -5}, core.Pricing{InputPerMTok: 1_000_000}); got != 0 {
		t.Fatalf("negative tokens billed %d", got)
	}
	// two terms that each fit but sum past MaxInt64
	p := core.Pricing{InputPerMTok: math.MaxInt64 / 2, OutputPerMTok: math.MaxInt64 / 2}
	if got := core.Cost(core.Usage{InputTokens: 1, OutputTokens: 3}, p); got != core.MicroUSD(math.MaxInt64) {
		t.Fatalf("sum overflow = %d", got)
	}
}

func TestToolResultBlock(t *testing.T) {
	b := core.ToolResult{ToolCallID: "c1", Content: core.Text("ok"), IsError: true}.Block()
	if b.ToolUseID != "c1" || !b.IsError || b.Content.PlainText() != "ok" {
		t.Fatalf("%#v", b)
	}
}

func TestProviderErrorClassification(t *testing.T) {
	cases := []struct {
		name            string
		e               core.ProviderError
		retry, failover bool
	}{
		{"rate limit", core.ProviderError{Kind: core.KindRateLimit, Status: 429}, true, true},
		{"connection", core.ProviderError{Kind: core.KindConnection}, true, true},
		{"500", core.ProviderError{Kind: core.KindStatus, Status: 500}, true, true},
		{"529 overloaded", core.ProviderError{Kind: core.KindStatus, Status: 529}, true, true},
		{"408", core.ProviderError{Kind: core.KindStatus, Status: 408}, true, false},
		{"409", core.ProviderError{Kind: core.KindStatus, Status: 409}, true, false},
		{"404 model", core.ProviderError{Kind: core.KindNotFound, Status: 404}, false, true},
		{"400", core.ProviderError{Kind: core.KindInvalid, Status: 400}, false, false},
		{"401", core.ProviderError{Kind: core.KindAuth, Status: 401}, false, false},
		{"418", core.ProviderError{Kind: core.KindStatus, Status: 418}, false, false},
	}
	for _, c := range cases {
		e := c.e
		if e.Retryable() != c.retry || e.Failover() != c.failover {
			t.Errorf("%s: retry=%v failover=%v", c.name, e.Retryable(), e.Failover())
		}
	}
}

func TestIsRetryableUnwraps(t *testing.T) {
	pe := &core.ProviderError{Provider: "anthropic", Kind: core.KindRateLimit, Status: 429, Message: "slow down"}
	wrapped := errors.Join(errors.New("ctx"), pe)
	if !core.IsRetryable(wrapped) {
		t.Fatal("wrapped rate limit not retryable")
	}
	if core.IsRetryable(errors.New("plain")) || core.IsRetryable(nil) {
		t.Fatal("plain error retryable")
	}
	cause := errors.New("eof")
	if !errors.Is(&core.ProviderError{Cause: cause}, cause) {
		t.Fatal("Cause not unwrapped")
	}
	if !strings.Contains(pe.Error(), "anthropic") || !strings.Contains(pe.Error(), "429") {
		t.Fatalf("message: %s", pe.Error())
	}
}

func TestBudgetExceededMessage(t *testing.T) {
	var err error = &core.BudgetExceededError{Budget: "steps", Limit: 5, Used: 6}
	var be *core.BudgetExceededError
	if !errors.As(err, &be) || !strings.Contains(err.Error(), "steps") || !strings.Contains(err.Error(), "6 of 5") {
		t.Fatal(err)
	}
}

func TestNewID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := core.NewID("run")
		if !strings.HasPrefix(id, "run_") || len(id) != len("run_")+24 {
			t.Fatalf("bad id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate %q", id)
		}
		seen[id] = true
	}
	if id := core.NewID(""); len(id) != 24 || strings.Contains(id, "_") {
		t.Fatalf("unprefixed id %q", id)
	}
}

func TestCancelSource(t *testing.T) {
	s := core.NewCancelSource()
	tok := s.Token()
	if tok.Cancelled() || tok.Reason() != "" {
		t.Fatal("new token already cancelled")
	}
	select {
	case <-tok.Done():
		t.Fatal("Done closed early")
	default:
	}
	s.Cancel("kill switch")
	s.Cancel("second reason is ignored")
	if !tok.Cancelled() || tok.Reason() != "kill switch" {
		t.Fatalf("cancelled=%v reason=%q", tok.Cancelled(), tok.Reason())
	}
	select {
	case <-tok.Done():
	default:
		t.Fatal("Done not closed")
	}
}

func TestContextFollowsTokenAndParent(t *testing.T) {
	s := core.NewCancelSource()
	ctx, release := core.Context(context.Background(), s.Token())
	defer release()
	if ctx.Err() != nil {
		t.Fatal("cancelled early")
	}
	s.Cancel("x")
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("token cancel did not reach the context")
	}

	// the parent cancelling also cancels the derived context
	parent, cancelParent := context.WithCancel(context.Background())
	ctx2, release2 := core.Context(parent, core.NewCancelSource().Token())
	defer release2()
	cancelParent()
	select {
	case <-ctx2.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("parent cancel did not reach the context")
	}
}

func TestRunStatusTerminal(t *testing.T) {
	for _, s := range []core.RunStatus{core.RunSucceeded, core.RunFailed, core.RunCancelled} {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []core.RunStatus{core.RunPending, core.RunRunning, core.RunAwaitingTool, core.RunAwaitingApproval, core.RunBlockedCredits} {
		if s.Terminal() {
			t.Errorf("%s must not be terminal (a blocked run resumes)", s)
		}
	}
}

func TestRoleValid(t *testing.T) {
	for _, r := range []core.Role{core.RoleSystem, core.RoleUser, core.RoleAssistant, core.RoleTool} {
		if !r.Valid() {
			t.Errorf("%s invalid", r)
		}
	}
	if core.Role("").Valid() || core.Role("x").Valid() {
		t.Fatal("unknown role valid")
	}
}

// The ports are interfaces the host binds; this pins their method sets so a
// signature drift breaks the build here.
type (
	credStore  struct{}
	usageSink  struct{}
	respCache  struct{}
	msgStore   struct{}
	stepStore  struct{}
	callStore  struct{}
	promptStor struct{}
	promptVer  struct{}
)

func (credStore) Get(context.Context, string) (map[string]string, error) {
	return nil, core.ErrNotFound
}
func (usageSink) Record(context.Context, core.UsageEvent) error                        { return nil }
func (respCache) Get(context.Context, string) (*core.ChatResponse, error)              { return nil, nil }
func (respCache) Set(context.Context, string, *core.ChatResponse, time.Duration) error { return nil }
func (msgStore) Append(context.Context, string, core.Message) error                    { return nil }
func (msgStore) List(context.Context, string) ([]core.Message, error)                  { return nil, nil }
func (stepStore) Save(context.Context, core.StepRecord) error                          { return nil }
func (stepStore) List(context.Context, string) ([]core.StepRecord, error)              { return nil, nil }
func (stepStore) SetStatus(context.Context, string, core.RunStatus, string) error      { return nil }
func (callStore) Save(context.Context, core.ToolCallRecord) error                      { return nil }
func (callStore) List(context.Context, string) ([]core.ToolCallRecord, error)          { return nil, nil }
func (promptStor) GetByKey(context.Context, string, string) (*core.PromptTemplate, error) {
	return nil, core.ErrNotFound
}
func (promptVer) Get(context.Context, string) (*core.PromptVersion, error)       { return nil, nil }
func (promptVer) GetActive(context.Context, string) (*core.PromptVersion, error) { return nil, nil }

var (
	_ core.CredentialStore    = credStore{}
	_ core.UsageSink          = usageSink{}
	_ core.ResponseCache      = respCache{}
	_ core.MessageStore       = msgStore{}
	_ core.AgentStepStore     = stepStore{}
	_ core.AgentToolCallStore = callStore{}
	_ core.PromptStore        = promptStor{}
	_ core.PromptVersionStore = promptVer{}
	_ core.CancelToken        = core.NewCancelSource().Token()
)

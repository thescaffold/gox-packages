package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
	aitesting "github.com/thescaffold/gox-packages/libs/ai/testing"
)

var planSchema = map[string]any{
	"type":                 "object",
	"required":             []any{"title", "steps"},
	"additionalProperties": false,
	"properties": map[string]any{
		"title": map[string]any{"type": "string", "minLength": float64(1)},
		"steps": map[string]any{"type": "array", "minItems": float64(1), "items": map[string]any{"type": "string"}},
	},
}

type plan struct {
	Title string   `json:"title"`
	Steps []string `json:"steps"`
}

func sreq() llm.StructuredRequest {
	return llm.StructuredRequest{ChatRequest: llm.ChatRequest{Model: aitesting.FakeModel, Messages: []core.Message{core.UserText("plan it")}}, Schema: planSchema, Description: "the plan"}
}

func TestStructuredReturnsAMatchingReplyAtOnce(t *testing.T) {
	f := aitesting.NewFake(aitesting.ToolCall("t1", "respond", plan{Title: "Ship", Steps: []string{"build"}}))
	var got plan
	res, err := llm.StructuredInto(context.Background(), f, sreq(), &got)
	if err != nil || got.Title != "Ship" || res.Attempts != 1 || len(res.Problems) != 0 {
		t.Fatalf("%v %+v %+v", err, got, res)
	}
	c := f.Calls()[0]
	if len(c.Tools) != 1 || c.Tools[0].Name != "respond" || !c.Tools[0].Strict || c.ToolChoice.Mode != llm.ToolOne || c.ToolChoice.Name != "respond" {
		t.Errorf("the answer should be asked for through one forced tool: %+v %+v", c.Tools, c.ToolChoice)
	}
}

func TestStructuredRepairsAReplyThatDoesNotFitAndTellsTheModelWhy(t *testing.T) {
	f := aitesting.NewFake(
		aitesting.ToolCall("t1", "respond", map[string]any{"title": "Ship"}), // steps missing
		aitesting.ToolCall("t2", "respond", plan{Title: "Ship", Steps: []string{"build"}}),
	)
	res, err := llm.Structured(context.Background(), f, sreq())
	if err != nil || res.Attempts != 2 || len(res.Problems) != 1 || !strings.Contains(res.Problems[0], `missing required property "steps"`) {
		t.Fatalf("%v %+v", err, res)
	}
	// the second call carries the first reply and an error result that says what was wrong
	second := f.Calls()[1].Messages
	if len(second) != 3 || second[1].Role != core.RoleAssistant {
		t.Fatalf("messages: %+v", second)
	}
	tr, ok := second[2].Content[0].(core.ToolResultBlock)
	if !ok || tr.ToolUseID != "t1" || !tr.IsError || !strings.Contains(tr.Content.PlainText(), "steps") {
		t.Errorf("the repair: %+v", second[2])
	}
	// usage is every attempt's
	if res.Usage.InputTokens != 20 || res.Usage.OutputTokens != 16 {
		t.Errorf("usage: %+v", res.Usage)
	}
}

// The done-criterion: malformed output is repaired or fails cleanly, after at most two repairs.
func TestStructuredFailsCleanlyAfterTwoRepairs(t *testing.T) {
	bad := aitesting.ToolCall("t", "respond", map[string]any{"title": 5})
	f := aitesting.NewFake(bad)
	res, err := llm.Structured(context.Background(), f, sreq())
	var se *llm.StructuredError
	if res != nil || !errors.As(err, &se) || !errors.Is(err, llm.ErrStructured) {
		t.Fatalf("%v %v", res, err)
	}
	if se.Attempts != 3 || f.CallCount() != 3 || len(se.Problems) != 3 || se.Usage.InputTokens != 30 {
		t.Errorf("attempts %d calls %d problems %v usage %+v", se.Attempts, f.CallCount(), se.Problems, se.Usage)
	}
	if !strings.Contains(err.Error(), "3 attempt") {
		t.Errorf("message: %v", err)
	}
}

func TestStructuredRepairCountCanBeChanged(t *testing.T) {
	bad := aitesting.ToolCall("t", "respond", map[string]any{})
	f := aitesting.NewFake(bad)
	r := sreq()
	r.Repairs = -1
	if _, err := llm.Structured(context.Background(), f, r); err == nil || f.CallCount() != 1 {
		t.Errorf("no repairs: %v, %d calls", err, f.CallCount())
	}
	f = aitesting.NewFake(bad)
	r.Repairs = 4
	_, _ = llm.Structured(context.Background(), f, r)
	if f.CallCount() != 5 {
		t.Errorf("four repairs: %d calls", f.CallCount())
	}
}

func TestStructuredAcceptsJSONWrittenAsText(t *testing.T) {
	for name, text := range map[string]string{
		"plain":  `{"title":"Ship","steps":["a"]}`,
		"fenced": "```json\n{\"title\":\"Ship\",\"steps\":[\"a\"]}\n```",
	} {
		res, err := llm.Structured(context.Background(), aitesting.NewFake(aitesting.Reply(text)), sreq())
		if err != nil || res.Attempts != 1 {
			t.Errorf("%s: %v", name, err)
		}
	}
	// prose is not an answer: it is sent back, with a plain nudge
	f := aitesting.NewFake(aitesting.Reply("Sure! Here is the plan."), aitesting.ToolCall("t", "respond", plan{Title: "x", Steps: []string{"y"}}))
	res, err := llm.Structured(context.Background(), f, sreq())
	if err != nil || res.Attempts != 2 {
		t.Fatalf("%v %+v", err, res)
	}
	if last := f.Calls()[1].Messages[2]; last.Role != core.RoleUser || !strings.Contains(last.Content.PlainText(), "not JSON") {
		t.Errorf("nudge: %+v", last)
	}
}

func TestStructuredAnswersEveryToolCallOfAReplyThatNeedsRepair(t *testing.T) {
	two := aitesting.Turn{Reply: &core.ChatResponse{StopReason: core.StopToolUse, Message: core.Message{Role: core.RoleAssistant, Content: core.Content{
		core.ToolUseBlock{ID: "a", Name: "other", Input: json.RawMessage(`{}`)},
		core.ToolUseBlock{ID: "b", Name: "respond", Input: json.RawMessage(`{"title":""}`)},
	}}}}
	f := aitesting.NewFake(two, aitesting.ToolCall("c", "respond", plan{Title: "x", Steps: []string{"y"}}))
	if _, err := llm.Structured(context.Background(), f, sreq()); err != nil {
		t.Fatal(err)
	}
	m := f.Calls()[1].Messages[2].Content
	if len(m) != 2 {
		t.Fatalf("both calls need a result: %+v", m)
	}
	a, b := m[0].(core.ToolResultBlock), m[1].(core.ToolResultBlock)
	if a.ToolUseID != "a" || b.ToolUseID != "b" || !strings.Contains(b.Content.PlainText(), "steps") {
		t.Errorf("%+v %+v", a, b)
	}
}

func TestStructuredCheckHookGivesTheModelTheReason(t *testing.T) {
	f := aitesting.NewFake(
		aitesting.ToolCall("1", "respond", plan{Title: "Ship", Steps: []string{"a"}}),
		aitesting.ToolCall("2", "respond", plan{Title: "Ship", Steps: []string{"a", "b"}}),
	)
	r := sreq()
	r.Check = func(raw json.RawMessage) error {
		var p plan
		_ = json.Unmarshal(raw, &p)
		if len(p.Steps) < 2 {
			return errors.New("a plan needs at least two steps")
		}
		return nil
	}
	res, err := llm.Structured(context.Background(), f, r)
	if err != nil || res.Attempts != 2 || !strings.Contains(res.Problems[0], "at least two steps") {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestStructuredStopsAtOnceOnARefusalAndOnProviderErrors(t *testing.T) {
	refuse := aitesting.Turn{Reply: &core.ChatResponse{Message: core.AssistantText("no"), StopReason: core.StopRefusal, Usage: core.Usage{InputTokens: 7}}}
	f := aitesting.NewFake(refuse)
	_, err := llm.Structured(context.Background(), f, sreq())
	var se *llm.StructuredError
	if !errors.As(err, &se) || !se.Refused || f.CallCount() != 1 || se.Usage.InputTokens != 7 {
		t.Fatalf("%v", err)
	}
	boom := &core.ProviderError{Kind: core.ErrorKind("rate_limit"), Message: "slow down"}
	f = aitesting.NewFake(aitesting.Fail(boom))
	if _, err := llm.Structured(context.Background(), f, sreq()); !errors.Is(err, boom) || f.CallCount() != 1 {
		t.Errorf("provider error: %v (%d calls)", err, f.CallCount())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := llm.Structured(ctx, aitesting.NewFake(aitesting.Reply("{}")), sreq()); err == nil {
		t.Error("a cancelled context should fail")
	}
}

func TestStructuredTruncatedReplyIsRetriedNotTrusted(t *testing.T) {
	cut := aitesting.ToolCall("1", "respond", plan{Title: "Ship", Steps: []string{"a"}})
	cut.Reply.StopReason = core.StopMaxTokens
	f := aitesting.NewFake(cut, aitesting.ToolCall("2", "respond", plan{Title: "Ship", Steps: []string{"a"}}))
	res, err := llm.Structured(context.Background(), f, sreq())
	if err != nil || res.Attempts != 2 || !strings.Contains(res.Problems[0], "cut off") {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestStructuredAsksNicelyWhenTheModelCannotBeForced(t *testing.T) {
	f := aitesting.NewFake(aitesting.ToolCall("1", "respond", plan{Title: "x", Steps: []string{"y"}}))
	f.Info = &llm.ModelInfo{ID: aitesting.FakeModel, Provider: "fake", MaxOutputTokens: 1000, SupportsTools: true, SupportsForcedToolChoice: false}
	if _, err := llm.Structured(context.Background(), f, sreq()); err != nil {
		t.Fatal(err)
	}
	c := f.Calls()[0]
	if c.ToolChoice.Mode != llm.ToolAuto || len(c.System) != 1 || !strings.Contains(c.System[0].Text, "respond") {
		t.Errorf("%+v %+v", c.ToolChoice, c.System)
	}
}

func TestStructuredDoesNotChangeWhatItWasGiven(t *testing.T) {
	f := aitesting.NewFake(aitesting.ToolCall("1", "respond", map[string]any{}), aitesting.ToolCall("2", "respond", plan{Title: "x", Steps: []string{"y"}}))
	r := sreq()
	msgs := r.Messages
	if _, err := llm.Structured(context.Background(), f, r); err != nil {
		t.Fatal(err)
	}
	if len(r.Messages) != 1 || len(msgs) != 1 || len(r.Tools) != 0 {
		t.Errorf("the caller's request changed: %+v", r)
	}
	if _, err := llm.Structured(context.Background(), f, llm.StructuredRequest{ChatRequest: r.ChatRequest}); !errors.Is(err, llm.ErrInvalidRequest) {
		t.Errorf("no schema: %v", err)
	}
}

func TestStructuredIntoReportsAShapeThatDoesNotDecode(t *testing.T) {
	f := aitesting.NewFake(aitesting.ToolCall("1", "respond", map[string]any{"title": "x", "steps": []string{"y"}}))
	var wrong struct {
		Title int `json:"title"`
	}
	if _, err := llm.StructuredInto(context.Background(), f, sreq(), &wrong); err == nil || !strings.Contains(err.Error(), "Go type") {
		t.Errorf("%v", err)
	}
}

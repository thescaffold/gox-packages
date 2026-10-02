package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/thescaffold/gox-packages/libs/ai/core"
)

// DefaultRepairs is how many times a reply that does not match is sent back to
// the model with what was wrong (TRD §6.5: at most two repair turns).
const DefaultRepairs = 2

// StructuredRequest is a model call whose answer must be JSON that matches a
// schema. The answer is asked for as the input of one tool, so the provider's own
// strict tool input does most of the work; whatever comes back is validated here
// again, because a model can still return something that does not fit.
type StructuredRequest struct {
	ChatRequest
	// Name of the tool the answer is given through; "respond" when empty.
	Name        string
	Description string
	// Schema is a JSON Schema object (the subset core.ValidateInput understands).
	Schema map[string]any
	// Repairs is how many repair turns are allowed after the first reply: 0 means
	// DefaultRepairs, a negative number means none.
	Repairs int
	// Check is an optional further test of a reply that fits the schema (an id that must
	// exist, two fields that must agree). Its error text is what the model is told.
	Check func(raw json.RawMessage) error
}

// StructuredResult is a reply that fit.
type StructuredResult struct {
	// Raw is the JSON that matched.
	Raw json.RawMessage
	// Attempts is how many model calls it took (1 when the first reply fit).
	Attempts int
	// Problems lists what was wrong with each reply that was sent back, oldest first.
	Problems []string
	// Usage is the tokens of every attempt together, the repaired ones too.
	Usage core.Usage
	// Response is the last reply.
	Response *core.ChatResponse
}

// StructuredError is a reply that never fit. It carries what was spent, so the
// caller can still charge for it.
type StructuredError struct {
	Attempts int
	Problems []string
	Usage    core.Usage
	// Refused: the model declined to answer, which is not retried.
	Refused bool
}

func (e *StructuredError) Error() string {
	if e.Refused {
		return "llm: the model declined to answer"
	}
	last := ""
	if n := len(e.Problems); n > 0 {
		last = ": " + e.Problems[n-1]
	}
	return fmt.Sprintf("llm: no reply matched the schema after %d attempt(s)%s", e.Attempts, last)
}

// ErrStructured lets a caller test for a StructuredError with errors.Is.
var ErrStructured = errors.New("llm: structured output failed")

func (e *StructuredError) Is(target error) bool { return target == ErrStructured }

// Structured asks the model for JSON that matches req.Schema. A reply that does
// not fit goes back to the model, with what was wrong, up to the allowed repairs;
// after that the call fails with a *StructuredError rather than returning
// something unchecked. A refusal, a provider error and a cancelled context end it
// at once (the runtime's retry and fallback rules apply to those, not this).
func Structured(ctx context.Context, d ProviderDriver, req StructuredRequest) (*StructuredResult, error) {
	if req.Schema == nil {
		return nil, invalid("a structured request needs a schema")
	}
	name := req.Name
	if name == "" {
		name = "respond"
	}
	repairs := req.Repairs
	switch {
	case repairs == 0:
		repairs = DefaultRepairs
	case repairs < 0:
		repairs = 0
	}

	r := req.ChatRequest
	r.Messages = append([]core.Message(nil), r.Messages...)
	r.Tools = append(append([]core.ToolDef(nil), r.Tools...), core.ToolDef{Name: name, Description: req.Description, InputSchema: req.Schema, Strict: true})
	if forcedToolOK(ctx, d, r.Model) {
		r.ToolChoice = ToolChoice{Mode: ToolOne, Name: name}
	} else {
		r.ToolChoice = ToolChoice{Mode: ToolAuto}
		r.System = append(append([]SystemBlock(nil), r.System...), SystemBlock{Text: "Give your answer by calling the " + name + " tool, once, and say nothing else."})
	}

	out := &StructuredResult{}
	for attempt := 0; attempt <= repairs; attempt++ {
		resp, err := d.Chat(ctx, r)
		if err != nil {
			return nil, err
		}
		out.Attempts++
		out.Usage = out.Usage.Add(resp.Usage)
		out.Response = resp
		if resp.StopReason == core.StopRefusal {
			return nil, &StructuredError{Attempts: out.Attempts, Problems: out.Problems, Usage: out.Usage, Refused: true}
		}

		raw, uses, problem := answerOf(resp, name)
		if problem == "" {
			problem = fit(req, raw)
		}
		if problem == "" {
			out.Raw = raw
			return out, nil
		}
		out.Problems = append(out.Problems, problem)
		if attempt == repairs {
			break
		}
		r.Messages = append(r.Messages, resp.Message, repairMessage(uses, name, problem))
	}
	return nil, &StructuredError{Attempts: out.Attempts, Problems: out.Problems, Usage: out.Usage}
}

// StructuredInto is Structured, then decodes the answer into out.
func StructuredInto(ctx context.Context, d ProviderDriver, req StructuredRequest, out any) (*StructuredResult, error) {
	res, err := Structured(ctx, d, req)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(res.Raw, out); err != nil {
		return nil, fmt.Errorf("llm: the answer fits the schema but not the Go type: %w", err)
	}
	return res, nil
}

func forcedToolOK(ctx context.Context, d ProviderDriver, model string) bool {
	infos, err := d.Models(ctx)
	if err != nil {
		return true
	}
	for _, m := range infos {
		if m.ID == model {
			return m.SupportsForcedToolChoice
		}
	}
	return true
}

// answerOf finds the answer in a reply: the input of the tool, or (for a model that
// wrote it out instead) a JSON object in the text. uses are the tool calls the
// reply made, which a repair has to answer. A reply cut off by the length limit is
// a problem of its own, whatever it holds.
func answerOf(resp *core.ChatResponse, name string) (raw json.RawMessage, uses []core.ToolUseBlock, problem string) {
	uses = resp.Message.Content.ToolUses()
	if resp.StopReason == core.StopMaxTokens {
		return nil, uses, "the reply was cut off before it was complete; answer again, shorter"
	}
	for _, u := range uses {
		if u.Name == name {
			return u.Input, uses, ""
		}
	}
	if len(uses) > 0 {
		return nil, uses, "the answer must be given by calling the " + name + " tool"
	}
	text := strings.TrimSpace(resp.Message.Content.PlainText())
	if j := jsonIn(text); j != "" {
		return json.RawMessage(j), nil, ""
	}
	return nil, nil, "the reply was not JSON; answer by calling the " + name + " tool"
}

// jsonIn returns the JSON object in text, which may be fenced (```json ... ```), or "".
func jsonIn(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "```") {
		t = strings.TrimPrefix(t, "```")
		t = strings.TrimPrefix(t, "json")
		if i := strings.LastIndex(t, "```"); i >= 0 {
			t = t[:i]
		}
		t = strings.TrimSpace(t)
	}
	if strings.HasPrefix(t, "{") && json.Valid([]byte(t)) {
		return t
	}
	return ""
}

func fit(req StructuredRequest, raw json.RawMessage) string {
	if err := core.ValidateInput(req.Schema, raw); err != nil {
		return err.Error()
	}
	if req.Check != nil {
		if err := req.Check(raw); err != nil {
			return err.Error()
		}
	}
	return ""
}

// repairMessage tells the model what was wrong. Every tool call of the reply has to be
// answered, so each gets a result: an error for the one that held the answer.
func repairMessage(uses []core.ToolUseBlock, name, problem string) core.Message {
	if len(uses) == 0 {
		return core.UserText("That did not work: " + problem + ". Answer again.")
	}
	var blocks core.Content
	said := false
	for _, u := range uses {
		text := "not used"
		if u.Name == name && !said {
			text, said = "That did not fit: "+problem+". Call the tool again with a corrected answer.", true
		}
		blocks = append(blocks, core.ToolResultBlock{ToolUseID: u.ID, Content: core.Text(text), IsError: true})
	}
	if !said {
		blocks = append(blocks, core.TextBlock{Text: "That did not work: " + problem + ". Answer again."})
	}
	return core.Message{Role: core.RoleUser, Content: blocks}
}

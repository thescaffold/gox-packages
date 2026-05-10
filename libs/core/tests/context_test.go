package tests

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	test "github.com/awesome-goose/goose/testing"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
)

func TestContext(t *testing.T) {
	test.NewSuiteRunner(t, &ContextSuite{}).Run()
}

type ContextSuite struct {
	test.Suite
}

func b64JSON(v any) string {
	b, _ := json.Marshal(v)
	return base64.StdEncoding.EncodeToString(b)
}

func (s *ContextSuite) TestParse_PlainHeaders() {
	headers := map[string][]string{
		"x-ntx-method":       {"GET"},
		"x-ntx-source":       {"my-service"},
		"x-ntx-sink":         {"other-service"},
		"x-ntx-tracing-id":   {"tid-123"},
		"x-ntx-user-id":      {"user-1"},
		"x-ntx-client-id":    {"client-1"},
		"x-ntx-workspace-id": {"ws-1"},
		"x-ntx-scope":        {"workspace"},
	}
	ctx := ntxctx.Parse(headers)
	s.T.Expect(ctx.Method).ToEqual("GET")
	s.T.Expect(ctx.Source).ToEqual("my-service")
	s.T.Expect(ctx.Sink).ToEqual("other-service")
	s.T.Expect(ctx.TracingID).ToEqual("tid-123")
	s.T.Expect(ctx.UserID).ToEqual("user-1")
	s.T.Expect(ctx.ClientID).ToEqual("client-1")
	s.T.Expect(ctx.WorkspaceID).ToEqual("ws-1")
	s.T.Expect(ctx.Scope).ToEqual("workspace")
}

func (s *ContextSuite) TestParse_Base64Fields() {
	user := map[string]any{"id": "u1", "name": "Alice"}
	roles := []any{"admin", "member"}
	headers := map[string][]string{
		"x-ntx-user":  {b64JSON(user)},
		"x-ntx-roles": {b64JSON(roles)},
	}
	ctx := ntxctx.Parse(headers)
	s.T.Expect(ctx.User["id"]).ToEqual("u1")
	s.T.Expect(ctx.User["name"]).ToEqual("Alice")
	s.T.Expect(len(ctx.Roles)).ToEqual(2)
}

func (s *ContextSuite) TestParse_MissingTracingID_IsGenerated() {
	ctx := ntxctx.Parse(map[string][]string{})
	s.T.Expect(ctx.TracingID).Not().ToEqual("")
}

func (s *ContextSuite) TestParse_NilBase64_ReturnsNil() {
	ctx := ntxctx.Parse(map[string][]string{})
	s.T.Expect(ctx.User).ToBeNil()
	s.T.Expect(ctx.Roles).ToBeNil()
}

func (s *ContextSuite) TestFormat_RoundTrip() {
	original := ntxctx.NTXContext{
		Method:      "POST",
		Source:      "svc-a",
		Sink:        "svc-b",
		TracingID:   "tid-999",
		UserID:      "u1",
		ClientID:    "c1",
		WorkspaceID: "w1",
		Scope:       "global",
		User:        map[string]any{"id": "u1"},
		Roles:       []any{"admin"},
	}
	headers := ntxctx.Format(original)

	// Turn single-value map back into Headers format
	hdrs := make(map[string][]string)
	for k, v := range headers {
		hdrs[k] = []string{v}
	}
	reparsed := ntxctx.Parse(hdrs)

	s.T.Expect(reparsed.Method).ToEqual("POST")
	s.T.Expect(reparsed.UserID).ToEqual("u1")
	s.T.Expect(reparsed.Scope).ToEqual("global")
	s.T.Expect(reparsed.User["id"]).ToEqual("u1")
	s.T.Expect(len(reparsed.Roles)).ToEqual(1)
}

func (s *ContextSuite) TestFormat_NilFieldsOmitted() {
	ctx := ntxctx.NTXContext{Source: "src"}
	h := ntxctx.Format(ctx)
	_, hasUser := h["x-ntx-user"]
	s.T.Expect(hasUser).ToEqual(false)
}

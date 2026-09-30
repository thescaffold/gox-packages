package core_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thescaffold/gox-packages/libs/ai/core"
)

var writeFile = map[string]any{
	"type":                 "object",
	"required":             []any{"path", "content"},
	"additionalProperties": false,
	"properties": map[string]any{
		"path":    map[string]any{"type": "string", "minLength": 1, "maxLength": 20},
		"content": map[string]any{"type": "string"},
		"mode":    map[string]any{"type": "string", "enum": []any{"create", "overwrite"}},
		"lines":   map[string]any{"type": "array", "minItems": 1, "maxItems": 2, "items": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}},
		"opts":    map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "boolean"}},
		"n":       map[string]any{"type": []any{"number", "null"}},
		"kind":    map[string]any{"const": "file"},
	},
}

func TestValidateInput(t *testing.T) {
	good := []string{
		`{"path":"a.txt","content":""}`,
		`{"path":"a.txt","content":"x","mode":"create","lines":[1,2],"opts":{"a":true},"n":null,"kind":"file"}`,
		`{"path":"a.txt","content":"x","n":1.5}`,
		`{"path":"a.txt","content":"x","lines":[2.0]}`, // 2.0 is an integer value
		"  {\"path\":\"é\",\"content\":\"x\"}  ",
	}
	for _, in := range good {
		if err := core.ValidateInput(writeFile, json.RawMessage(in)); err != nil {
			t.Errorf("rejected %s: %v", in, err)
		}
	}
	bad := map[string]string{
		`{"path":"a.txt"}`:                               "missing required",
		`{"path":"","content":"x"}`:                      "shorter",
		`{"path":"aaaaaaaaaaaaaaaaaaaaa","content":"x"}`: "longer",
		`{"path":1,"content":"x"}`:                       "want string",
		`{"path":"a","content":"x","extra":1}`:           "unexpected property",
		`{"path":"a","content":"x","mode":"delete"}`:     "allowed values",
		`{"path":"a","content":"x","lines":[]}`:          "at least",
		`{"path":"a","content":"x","lines":[1,2,3]}`:     "at most",
		`{"path":"a","content":"x","lines":[0]}`:         "minimum",
		`{"path":"a","content":"x","lines":[101]}`:       "maximum",
		`{"path":"a","content":"x","lines":[1.5]}`:       "want integer",
		`{"path":"a","content":"x","opts":{"a":"yes"}}`:  "want boolean",
		`{"path":"a","content":"x","n":"1"}`:             "want number or null",
		`{"path":"a","content":"x","kind":"dir"}`:        "must equal",
		`[]`:                            "want object",
		`{"path":"a","content":"x"} {}`: "trailing",
		`{"path":"a",`:                  "not valid JSON",
		``:                              "not valid JSON",
	}
	for in, want := range bad {
		err := core.ValidateInput(writeFile, json.RawMessage(in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want it to mention %q", in, err, want)
		}
	}
}

func TestValidateInputNilSchemaAcceptsAnyJSONButNotGarbage(t *testing.T) {
	if err := core.ValidateInput(nil, json.RawMessage(`[1,"x",{}]`)); err != nil {
		t.Fatal(err)
	}
	if err := core.ValidateInput(nil, json.RawMessage(`{`)); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestValidateInputIgnoresUnknownKeywords(t *testing.T) {
	schema := map[string]any{"type": "object", "$comment": "x", "patternProperties": map[string]any{}, "properties": map[string]any{"a": map[string]any{"type": "string", "format": "uri", "pattern": "^x"}}}
	if err := core.ValidateInput(schema, json.RawMessage(`{"a":"not a uri"}`)); err != nil {
		t.Fatalf("unknown keywords must not fail validation: %v", err)
	}
}

func TestValidateInputReportsTheFailingPath(t *testing.T) {
	err := core.ValidateInput(writeFile, json.RawMessage(`{"path":"a","content":"x","lines":[1,"two"]}`))
	if err == nil || !strings.Contains(err.Error(), "input.lines[1]") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnumComparesNumbersByValue(t *testing.T) {
	s := map[string]any{"enum": []any{1, "a"}}
	for _, in := range []string{`1`, `1.0`, `"a"`} {
		if err := core.ValidateInput(s, json.RawMessage(in)); err != nil {
			t.Errorf("%s: %v", in, err)
		}
	}
	if err := core.ValidateInput(s, json.RawMessage(`2`)); err == nil {
		t.Error("2 accepted")
	}
}

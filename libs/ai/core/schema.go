package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
)

// ValidateInput checks a tool's JSON input against its schema. It implements
// the subset of JSON Schema that tool definitions use: type (one or a list),
// properties, required, additionalProperties (false or a schema), items, enum,
// const, min/max for numbers, and min/maxLength, minItems/maxItems. Keywords it
// does not know are ignored, never treated as failures, so a richer schema
// still validates on what is understood.
//
// It exists because eager input streaming makes the provider stop validating
// tool input, and because a model can call a tool with input that does not
// match even when it is asked to: nothing runs on unchecked input.
func ValidateInput(schema map[string]any, raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("input is not valid JSON: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("input is not valid JSON: trailing data")
	}
	return validate(schema, v, "input")
}

func validate(schema map[string]any, v any, path string) error {
	if schema == nil {
		return nil
	}
	if want, ok := schema["type"]; ok {
		if !typeMatches(want, v) {
			return fmt.Errorf("%s: want %s, got %s", path, typeNames(want), kindOf(v))
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if jsonEq(e, v) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: %v is not one of the allowed values", path, v)
		}
	}
	if c, ok := schema["const"]; ok && !jsonEq(c, v) {
		return fmt.Errorf("%s: must equal %v", path, c)
	}

	switch x := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		if req, ok := stringList(schema["required"]); ok {
			for _, r := range req {
				if _, present := x[r]; !present {
					return fmt.Errorf("%s: missing required property %q", path, r)
				}
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if sub, ok := props[k].(map[string]any); ok {
				if err := validate(sub, x[k], path+"."+k); err != nil {
					return err
				}
				continue
			}
			switch ap := schema["additionalProperties"].(type) {
			case bool:
				if !ap {
					return fmt.Errorf("%s: unexpected property %q", path, k)
				}
			case map[string]any:
				if err := validate(ap, x[k], path+"."+k); err != nil {
					return err
				}
			}
		}
	case []any:
		if n, ok := number(schema["minItems"]); ok && float64(len(x)) < n {
			return fmt.Errorf("%s: needs at least %v items", path, n)
		}
		if n, ok := number(schema["maxItems"]); ok && float64(len(x)) > n {
			return fmt.Errorf("%s: allows at most %v items", path, n)
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, e := range x {
				if err := validate(items, e, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case string:
		n := float64(len([]rune(x)))
		if m, ok := number(schema["minLength"]); ok && n < m {
			return fmt.Errorf("%s: shorter than %v", path, m)
		}
		if m, ok := number(schema["maxLength"]); ok && n > m {
			return fmt.Errorf("%s: longer than %v", path, m)
		}
	case json.Number:
		f, err := x.Float64()
		if err != nil || math.IsInf(f, 0) {
			return fmt.Errorf("%s: number out of range", path)
		}
		if m, ok := number(schema["minimum"]); ok && f < m {
			return fmt.Errorf("%s: below the minimum %v", path, m)
		}
		if m, ok := number(schema["maximum"]); ok && f > m {
			return fmt.Errorf("%s: above the maximum %v", path, m)
		}
	}
	return nil
}

func kindOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case json.Number:
		if isInteger(x) {
			return "integer"
		}
		return "number"
	}
	return "unknown"
}

func isInteger(n json.Number) bool {
	if _, err := n.Int64(); err == nil {
		return true
	}
	f, err := n.Float64()
	return err == nil && f == math.Trunc(f) && !math.IsInf(f, 0)
}

func typeMatches(want any, v any) bool {
	for _, t := range typeList(want) {
		k := kindOf(v)
		if t == k || (t == "number" && k == "integer") {
			return true
		}
	}
	return false
}

func typeList(want any) []string {
	switch w := want.(type) {
	case string:
		return []string{w}
	case []any:
		var out []string
		for _, e := range w {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return w
	}
	return nil
}

func typeNames(want any) string { return strings.Join(typeList(want), " or ") }

func stringList(v any) ([]string, bool) {
	switch l := v.(type) {
	case []string:
		return l, true
	case []any:
		out := make([]string, 0, len(l))
		for _, e := range l {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// jsonEq compares two decoded JSON values, treating a json.Number and a Go
// number with the same value as equal.
func jsonEq(a, b any) bool {
	norm := func(v any) any {
		raw, _ := json.Marshal(v)
		var out any
		_ = json.Unmarshal(raw, &out) // numbers become float64 here, so 1 and 1.0 agree
		return out
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

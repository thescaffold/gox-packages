package design

import (
	"encoding/json"
	"reflect"
	"strings"
)

// Schema is the JSON Schema (draft 2020-12) of a Graph. It is built from the Go
// types, so it cannot drift from them.
func Schema() []byte {
	defs := map[string]any{}
	root := schemaOf(reflect.TypeOf(Graph{}), defs)
	doc := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "https://origine.run/schemas/design-graph-v1.json",
		"title":   "Origine design graph",
		"$defs":   defs,
	}
	for k, v := range root {
		doc[k] = v
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

var enums = map[reflect.Type][]string{}

func init() {
	for _, k := range NodeKinds {
		enums[reflect.TypeOf(NodeKind(""))] = append(enums[reflect.TypeOf(NodeKind(""))], string(k))
	}
	for _, k := range EdgeKinds {
		enums[reflect.TypeOf(EdgeKind(""))] = append(enums[reflect.TypeOf(EdgeKind(""))], string(k))
	}
}

func schemaOf(t reflect.Type, defs map[string]any) map[string]any {
	if vals, ok := enums[t]; ok {
		return map[string]any{"type": "string", "enum": vals}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Ptr:
		return schemaOf(t.Elem(), defs)
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem(), defs)}
	case reflect.Struct:
		name := t.Name()
		if name != "Graph" {
			if _, done := defs[name]; !done {
				defs[name] = nil // reserve, so a type that contains itself does not recurse
				defs[name] = structSchema(t, defs)
			}
			return map[string]any{"$ref": "#/$defs/" + name}
		}
		return structSchema(t, defs)
	}
	return map[string]any{}
}

func structSchema(t reflect.Type, defs map[string]any) map[string]any {
	props := map[string]any{}
	var required []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		props[name] = schemaOf(f.Type, defs)
		if !strings.Contains(opts, "omitempty") {
			required = append(required, name)
		}
	}
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

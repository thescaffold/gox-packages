// Package prompt renders versioned prompt templates, with tenant overrides
// (TRD §6.2, PLAN M1-29).
//
// Templates are plain text with a deliberately small syntax, so a tenant can
// edit one without being able to do anything surprising:
//
//	{{name}}                     a variable (dotted paths: {{task.title}})
//	{{#if name}}…{{else}}…{{/if}}     when name is truthy
//	{{#unless name}}…{{/unless}}
//	{{#each name}}…{{this}} {{@index}}…{{/each}}
//	\{{                          a literal {{
//
// A variable that is not supplied is an error, never an empty string: a prompt
// that silently lost its task description is worse than a failed render.
// Truthy means not false, nil, "", 0, or an empty list or map.
package prompt

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// ErrTooLong: the rendered text exceeded the caller's limit.
type ErrTooLong struct{ Limit, Got int }

func (e *ErrTooLong) Error() string {
	return fmt.Sprintf("prompt: rendered %d characters, limit is %d", e.Got, e.Limit)
}

type node interface{}

type (
	textNode struct{ s string }
	varNode  struct {
		path string
		line int
	}
	ifNode struct {
		path      string
		negate    bool
		then, els []node
		line      int
	}
	eachNode struct {
		path string
		body []node
		line int
	}
)

// Template is a parsed template.
type Template struct {
	nodes []node
	vars  []string
}

// Parse compiles a template body, reporting the line of a syntax error.
func Parse(body string) (*Template, error) {
	p := &parser{src: body, line: 1}
	nodes, end, err := p.block()
	if err != nil {
		return nil, err
	}
	if end != "" {
		return nil, fmt.Errorf("prompt: line %d: unexpected {{%s}}", p.line, end)
	}
	t := &Template{nodes: nodes}
	set := map[string]bool{}
	collect(nodes, set, false)
	for v := range set {
		t.vars = append(t.vars, v)
	}
	sort.Strings(t.vars)
	return t, nil
}

// Variables lists the top-level names the template reads, sorted. The names
// `this` and `@index` only make sense inside {{#each}} and are not listed.
func (t *Template) Variables() []string { return append([]string(nil), t.vars...) }

func collect(nodes []node, set map[string]bool, inEach bool) {
	add := func(path string) {
		root := strings.SplitN(path, ".", 2)[0]
		if root == "this" || root == "@index" {
			return
		}
		set[root] = true
	}
	for _, n := range nodes {
		switch v := n.(type) {
		case varNode:
			add(v.path)
		case ifNode:
			add(v.path)
			collect(v.then, set, inEach)
			collect(v.els, set, inEach)
		case eachNode:
			add(v.path)
			collect(v.body, set, true)
		}
	}
}

type parser struct {
	src  string
	pos  int
	line int
}

// block parses until an {{else}} / {{/x}} or the end, returning which closer stopped it.
func (p *parser) block() ([]node, string, error) {
	var out []node
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = append(out, textNode{text.String()})
			text.Reset()
		}
	}
	for p.pos < len(p.src) {
		rest := p.src[p.pos:]
		if strings.HasPrefix(rest, `\{{`) {
			text.WriteString("{{")
			p.pos += 3
			continue
		}
		if !strings.HasPrefix(rest, "{{") {
			if rest[0] == '\n' {
				p.line++
			}
			text.WriteByte(rest[0])
			p.pos++
			continue
		}
		end := strings.Index(rest, "}}")
		if end < 0 {
			return nil, "", fmt.Errorf("prompt: line %d: unclosed {{", p.line)
		}
		tag := strings.TrimSpace(rest[2:end])
		startLine := p.line
		p.line += strings.Count(rest[:end], "\n")
		p.pos += end + 2
		flush()
		switch {
		case tag == "":
			return nil, "", fmt.Errorf("prompt: line %d: empty {{}}", startLine)
		case tag == "else" || strings.HasPrefix(tag, "/"):
			return out, tag, nil
		case tag == "#if" || tag == "#unless" || tag == "#each":
			return nil, "", fmt.Errorf("prompt: line %d: {{%s}} is missing a name", startLine, tag)
		case strings.HasPrefix(tag, "#if ") || strings.HasPrefix(tag, "#unless "):
			neg := strings.HasPrefix(tag, "#unless ")
			name := strings.TrimSpace(tag[strings.Index(tag, " ")+1:])
			if err := checkPath(name, startLine); err != nil {
				return nil, "", err
			}
			closer := "/if"
			if neg {
				closer = "/unless"
			}
			then, stop, err := p.block()
			if err != nil {
				return nil, "", err
			}
			var els []node
			if stop == "else" {
				if els, stop, err = p.block(); err != nil {
					return nil, "", err
				}
			}
			if stop != closer {
				return nil, "", fmt.Errorf("prompt: line %d: {{#%s}} is not closed by {{%s}}", startLine, strings.TrimPrefix(closer, "/"), closer)
			}
			out = append(out, ifNode{path: name, negate: neg, then: then, els: els, line: startLine})
		case strings.HasPrefix(tag, "#each "):
			name := strings.TrimSpace(tag[len("#each "):])
			if err := checkPath(name, startLine); err != nil {
				return nil, "", err
			}
			body, stop, err := p.block()
			if err != nil {
				return nil, "", err
			}
			if stop != "/each" {
				return nil, "", fmt.Errorf("prompt: line %d: {{#each}} is not closed by {{/each}}", startLine)
			}
			out = append(out, eachNode{path: name, body: body, line: startLine})
		case strings.HasPrefix(tag, "#"):
			return nil, "", fmt.Errorf("prompt: line %d: unknown block {{%s}}", startLine, tag)
		default:
			if err := checkPath(tag, startLine); err != nil {
				return nil, "", err
			}
			out = append(out, varNode{path: tag, line: startLine})
		}
	}
	flush()
	return out, "", nil
}

func checkPath(s string, line int) error {
	if s == "" {
		return fmt.Errorf("prompt: line %d: missing name", line)
	}
	for _, seg := range strings.Split(s, ".") {
		if seg == "" {
			return fmt.Errorf("prompt: line %d: bad name %q", line, s)
		}
		for _, r := range seg {
			ok := r == '_' || r == '@' || r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
			if !ok {
				return fmt.Errorf("prompt: line %d: bad character %q in name %q", line, r, s)
			}
		}
	}
	return nil
}

// Render fills the template. maxLength 0 means no limit.
func (t *Template) Render(vars map[string]any, maxLength int) (string, error) {
	var b strings.Builder
	scope := &scope{vars: vars}
	if err := render(&b, t.nodes, scope, maxLength); err != nil {
		return "", err
	}
	return b.String(), nil
}

type scope struct {
	vars   map[string]any
	parent *scope
	this   any
	index  int
	inEach bool
}

func (s *scope) lookup(path string) (any, bool) {
	segs := strings.Split(path, ".")
	var cur any
	var found bool
	switch segs[0] {
	case "this", "@index":
		for sc := s; sc != nil; sc = sc.parent {
			if sc.inEach {
				if segs[0] == "this" {
					cur = sc.this
				} else {
					cur = sc.index
				}
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
		segs = segs[1:]
	default:
		for sc := s; sc != nil; sc = sc.parent {
			if v, ok := sc.vars[segs[0]]; ok {
				cur, found = v, true
				break
			}
		}
		if !found {
			return nil, false
		}
		segs = segs[1:]
	}
	for _, seg := range segs {
		next, ok := field(cur, seg)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func field(v any, name string) (any, bool) {
	if m, ok := v.(map[string]any); ok {
		x, ok := m[name]
		return x, ok
	}
	rv := reflect.ValueOf(v)
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map:
		if rv.Type().Key().Kind() == reflect.String {
			x := rv.MapIndex(reflect.ValueOf(name).Convert(rv.Type().Key()))
			if x.IsValid() {
				return x.Interface(), true
			}
		}
	case reflect.Struct:
		rt := rv.Type()
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag == name || (tag == "" && strings.EqualFold(f.Name, name)) {
				return rv.Field(i).Interface(), true
			}
		}
	}
	return nil, false
}

func truthy(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Bool:
		return rv.Bool()
	case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() > 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() != 0
	case reflect.Pointer, reflect.Interface:
		return !rv.IsNil()
	}
	return true
}

func format(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	case fmt.Stringer:
		return x.String(), nil
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprint(x), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func render(b *strings.Builder, nodes []node, sc *scope, max int) error {
	check := func() error {
		if max > 0 && b.Len() > max {
			return &ErrTooLong{Limit: max, Got: b.Len()}
		}
		return nil
	}
	for _, n := range nodes {
		switch v := n.(type) {
		case textNode:
			b.WriteString(v.s)
		case varNode:
			val, ok := sc.lookup(v.path)
			if !ok {
				return fmt.Errorf("prompt: line %d: variable %q was not supplied", v.line, v.path)
			}
			s, err := format(val)
			if err != nil {
				return fmt.Errorf("prompt: line %d: %q: %w", v.line, v.path, err)
			}
			b.WriteString(s)
		case ifNode:
			val, ok := sc.lookup(v.path)
			if !ok {
				return fmt.Errorf("prompt: line %d: variable %q was not supplied", v.line, v.path)
			}
			branch := v.then
			if truthy(val) == v.negate {
				branch = v.els
			}
			if err := render(b, branch, sc, max); err != nil {
				return err
			}
		case eachNode:
			val, ok := sc.lookup(v.path)
			if !ok {
				return fmt.Errorf("prompt: line %d: variable %q was not supplied", v.line, v.path)
			}
			rv := reflect.ValueOf(val)
			if val == nil || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
				if val == nil {
					continue
				}
				return fmt.Errorf("prompt: line %d: %q is not a list", v.line, v.path)
			}
			for i := 0; i < rv.Len(); i++ {
				inner := &scope{parent: sc, this: rv.Index(i).Interface(), index: i, inEach: true}
				if err := render(b, v.body, inner, max); err != nil {
					return err
				}
			}
		}
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

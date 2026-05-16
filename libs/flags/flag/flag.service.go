package flag

import (
	"fmt"

	corehttp "github.com/thescaffold/gox-packages/libs/core/http"
)

// Flag mirrors jsx-packages/libs/flags/src/common/utils/values.ts Flag.
type Flag struct {
	Name      string `json:"name"`
	Limit     int    `json:"limit"`
	Priority  int    `json:"priority"`
	Level     string `json:"level"`
	Reference string `json:"reference,omitempty"`
	Meta      any    `json:"meta,omitempty"`
	Status    string `json:"status,omitempty"`
}

// LogOpts holds optional context for a Log call. All fields are pointer-typed
// so omitted values are not sent to the server (matching TS optional params).
type LogOpts struct {
	Limit       *int
	Level       *string
	UserID      *string
	ClientID    *string
	WorkspaceID *string
}

// StatusOpts holds optional context for Status (and Limit) calls.
type StatusOpts struct {
	Level       *string
	UserID      *string
	ClientID    *string
	WorkspaceID *string
}

// LimitOpts holds optional context for a Limit call.
type LimitOpts = StatusOpts

// LimitResult mirrors jsx-flags limit() response.data:
// { allowed: boolean; limit: number; usage: number }.
type LimitResult struct {
	Allowed bool `json:"allowed"`
	Limit   int  `json:"limit"`
	Usage   int  `json:"usage"`
}

// Config is the subset of FlagsConfig that FlagService needs.
type Config struct {
	Server     string
	Credential string
	SourceId   string
}

// FlagService is an HTTP client for the scaffold flags server.
// Mirrors jsx-packages/libs/flags/src/flag/index.ts.
type FlagService struct {
	cfg    Config
	client *corehttp.Client
}

// NewFlagService creates a FlagService that POSTs to cfg.Server using cfg.Credential.
func NewFlagService(cfg Config, client *corehttp.Client) *FlagService {
	if client == nil {
		client = corehttp.New("")
	}
	return &FlagService{cfg: cfg, client: client}
}

// Register sends flag definitions to the server.
// environmentTypeName defaults to "Javascript" when empty, matching
// jsx-flags register() (jsx-packages/libs/flags/src/flag/index.ts:8).
// Returns the server's success boolean (mirrors TS register response.data: boolean).
func (s *FlagService) Register(flags []Flag, environmentTypeName string) (bool, error) {
	if environmentTypeName == "" {
		environmentTypeName = "Javascript"
	}
	body := map[string]any{
		"environmentType": map[string]any{"name": environmentTypeName},
		"environment":     map[string]any{"name": s.cfg.SourceId},
		"flags":           flags,
	}
	data, err := s.post("register", body)
	if err != nil {
		return false, err
	}
	if v, ok := data.(bool); ok {
		return v, nil
	}
	return false, nil
}

// Log records a usage event for the named flag.
func (s *FlagService) Log(name string, opts LogOpts) (bool, error) {
	body := map[string]any{
		"environment": map[string]any{"name": s.cfg.SourceId},
		"name":        name,
	}
	addOpt(body, "limit", opts.Limit)
	addOpt(body, "level", opts.Level)
	addOpt(body, "userId", opts.UserID)
	addOpt(body, "clientId", opts.ClientID)
	addOpt(body, "workspaceId", opts.WorkspaceID)

	data, err := s.post("log", body)
	if err != nil {
		return false, err
	}
	if v, ok := data.(bool); ok {
		return v, nil
	}
	return false, nil
}

// Status accepts names as: a single string, []string (1D), or [][]string (2D).
// Each shape is normalized to [][]string before sending, exactly like
// jsx-flags status() (lines 69-77). Returns the server's response.data.
func (s *FlagService) Status(names any, opts StatusOpts) (any, error) {
	normalized, ok := normalizeNames(names)
	if !ok {
		return false, nil
	}

	body := map[string]any{
		"environment": map[string]any{"name": s.cfg.SourceId},
		"names":       normalized,
	}
	addOpt(body, "level", opts.Level)
	addOpt(body, "userId", opts.UserID)
	addOpt(body, "clientId", opts.ClientID)
	addOpt(body, "workspaceId", opts.WorkspaceID)

	return s.post("status", body)
}

// Limit returns the {allowed, limit, usage} info for the named flag.
func (s *FlagService) Limit(name string, opts LimitOpts) (*LimitResult, error) {
	body := map[string]any{
		"environment": map[string]any{"name": s.cfg.SourceId},
		"name":        name,
	}
	addOpt(body, "level", opts.Level)
	addOpt(body, "userId", opts.UserID)
	addOpt(body, "clientId", opts.ClientID)
	addOpt(body, "workspaceId", opts.WorkspaceID)

	data, err := s.post("limit", body)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	m, ok := data.(map[string]any)
	if !ok {
		return nil, nil
	}
	out := &LimitResult{}
	if v, ok := m["allowed"].(bool); ok {
		out.Allowed = v
	}
	if v, ok := m["limit"].(float64); ok {
		out.Limit = int(v)
	}
	if v, ok := m["usage"].(float64); ok {
		out.Usage = int(v)
	}
	return out, nil
}

// post wraps a single bearer-auth POST and unwraps the envelope's `data` field.
// The URL is built by plain concatenation, matching jsx-flags
// `${config.server}/apps/flags/<action>` (no trailing-slash trimming).
func (s *FlagService) post(action string, body map[string]any) (any, error) {
	url := fmt.Sprintf("%s/apps/flags/%s", s.cfg.Server, action)
	headers := map[string]string{
		"authorization": "bearer " + s.cfg.Credential,
		"content-type":  "application/json",
	}
	ok, status, statusText, _, resp := s.client.Request("POST", url, body, nil, headers, 0)
	if !ok {
		return nil, fmt.Errorf("flags %s: %d %s", action, status, statusText)
	}
	if env, isMap := resp.(map[string]any); isMap {
		return env["data"], nil
	}
	return resp, nil
}

// normalizeNames converts the flexible names input to [][]string.
// Mirrors jsx-flags lines 69-77.
func normalizeNames(names any) ([][]string, bool) {
	switch v := names.(type) {
	case string:
		return [][]string{{v}}, true
	case []string:
		return [][]string{v}, true
	case [][]string:
		return v, true
	case []any:
		// a heterogeneous slice — try to coerce each element
		// to either a string (1D row) or another []any (2D inner row).
		all1D := true
		for _, el := range v {
			if _, ok := el.(string); !ok {
				all1D = false
				break
			}
		}
		if all1D {
			row := make([]string, len(v))
			for i, el := range v {
				row[i] = el.(string)
			}
			return [][]string{row}, true
		}
		out := make([][]string, 0, len(v))
		for _, el := range v {
			inner, ok := el.([]any)
			if !ok {
				return nil, false
			}
			row := make([]string, len(inner))
			for i, x := range inner {
				str, ok := x.(string)
				if !ok {
					return nil, false
				}
				row[i] = str
			}
			out = append(out, row)
		}
		return out, true
	}
	return nil, false
}

// addOpt sets body[key] = *p when p is non-nil. Mirrors TS optional query/body
// params that omit the key entirely when undefined.
func addOpt[T any](body map[string]any, key string, p *T) {
	if p != nil {
		body[key] = *p
	}
}

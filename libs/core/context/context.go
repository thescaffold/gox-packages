package context

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages/libs/core/utils"
)

// contextKey is the string key used to store NTXContext.
// Must match the `context:"ntx"` DTO tag that goose's input binder looks up.
const contextKey = "ntx"

// NTXContext holds all parsed x-ntx-* request headers.
type NTXContext struct {
	Method      string
	Source      string
	Sink        string
	TracingID   string
	UserID      string
	ClientID    string
	WorkspaceID string
	Config      utils.KeyValue
	User        utils.KeyValue
	Client      utils.KeyValue
	Workspace   utils.KeyValue
	Roles       []any
	Permissions []any
	Scope       string
	Preference  utils.KeyValue
	// IP holds the originating client IP, parsed from x-forwarded-for (first hop)
	// or x-real-ip. Mirrors TS get-ip.decorator.ts.
	IP string
}

// Parse reads x-ntx-* headers from a goose Headers map and returns a populated NTXContext.
func Parse(headers map[string][]string) NTXContext {
	get := func(key string) string {
		if vs, ok := headers[key]; ok && len(vs) > 0 {
			return vs[0]
		}
		return ""
	}
	decodeKV := func(h string) utils.KeyValue {
		if h == "" {
			return nil
		}
		raw, err := base64.StdEncoding.DecodeString(h)
		if err != nil {
			return nil
		}
		var m utils.KeyValue
		if err = json.Unmarshal(raw, &m); err != nil {
			return nil
		}
		return m
	}
	decodeSlice := func(h string) []any {
		if h == "" {
			return nil
		}
		raw, err := base64.StdEncoding.DecodeString(h)
		if err != nil {
			return nil
		}
		var s []any
		if err = json.Unmarshal(raw, &s); err != nil {
			return nil
		}
		return s
	}

	tracingID := get("x-ntx-tracing-id")
	if tracingID == "" {
		tracingID = utils.Reference("TID", 36)
	}
	source := get("x-ntx-source")

	return NTXContext{
		Method:      get("x-ntx-method"),
		Source:      source,
		Sink:        get("x-ntx-sink"),
		TracingID:   tracingID,
		UserID:      get("x-ntx-user-id"),
		ClientID:    get("x-ntx-client-id"),
		WorkspaceID: get("x-ntx-workspace-id"),
		Config:      decodeKV(get("x-ntx-config")),
		User:        decodeKV(get("x-ntx-user")),
		Client:      decodeKV(get("x-ntx-client")),
		Workspace:   decodeKV(get("x-ntx-workspace")),
		Roles:       decodeSlice(get("x-ntx-roles")),
		Permissions: decodeSlice(get("x-ntx-permissions")),
		Scope:       get("x-ntx-scope"),
		Preference:  decodeKV(get("x-ntx-preference")),
		IP:          parseIP(get("x-forwarded-for"), get("x-real-ip")),
	}
}

// parseIP returns the originating client IP from forwarding headers.
// X-Forwarded-For is a comma-separated list; the first entry is the original client.
// Mirrors TS getIP() in get-ip.decorator.ts.
func parseIP(forwardedFor, realIP string) string {
	if forwardedFor != "" {
		if idx := strings.Index(forwardedFor, ","); idx >= 0 {
			return strings.TrimSpace(forwardedFor[:idx])
		}
		return strings.TrimSpace(forwardedFor)
	}
	return strings.TrimSpace(realIP)
}

// Format serializes an NTXContext back into HTTP headers for outbound calls.
func Format(ctx NTXContext) map[string]string {
	out := map[string]string{
		"content-type":       "application/json",
		"accept":             "application/json",
		"x-ntx-method":       ctx.Method,
		"x-ntx-source":       ctx.Source,
		"x-ntx-sink":         ctx.Sink,
		"x-ntx-tracing-id":   ctx.TracingID,
		"x-ntx-user-id":      ctx.UserID,
		"x-ntx-client-id":    ctx.ClientID,
		"x-ntx-workspace-id": ctx.WorkspaceID,
		"x-ntx-scope":        ctx.Scope,
	}
	encodeKV := func(key string, v utils.KeyValue) {
		if v == nil {
			return
		}
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		out[key] = base64.StdEncoding.EncodeToString(b)
	}
	encodeSlice := func(key string, v []any) {
		if v == nil {
			return
		}
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		out[key] = base64.StdEncoding.EncodeToString(b)
	}
	encodeKV("x-ntx-config", ctx.Config)
	encodeKV("x-ntx-user", ctx.User)
	encodeKV("x-ntx-client", ctx.Client)
	encodeKV("x-ntx-workspace", ctx.Workspace)
	encodeSlice("x-ntx-roles", ctx.Roles)
	encodeSlice("x-ntx-permissions", ctx.Permissions)
	encodeKV("x-ntx-preference", ctx.Preference)
	return out
}

// Set stores an NTXContext in the goose context under the "ntx" key,
// which matches the `context:"ntx"` DTO tag used by goose's input binder.
func Set(ctx types.Context, ntx NTXContext) {
	ctx.SetValue(contextKey, ntx)
}

// Get retrieves the NTXContext from the goose context.
// Returns a zero-value NTXContext if not set.
func Get(ctx types.Context) NTXContext {
	v := ctx.GetValue(contextKey)
	if v == nil {
		return NTXContext{}
	}
	ntx, _ := v.(NTXContext)
	return ntx
}

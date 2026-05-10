package auth

import (
	"strings"

	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
)

// ExpandScopeObject mirrors TS expandScopeObject: walks every key in obj
// (dotted keys create nested maps), resolves each value with GetScopeValue.
func ExpandScopeObject(obj map[string]any, ctx ntxctx.NTXContext) map[string]any {
	result := map[string]any{}
	ctxMap := contextToMap(ctx)

	for key, value := range obj {
		parts := strings.Split(key, ".")
		current := result
		for i, part := range parts {
			if i == len(parts)-1 {
				current[part] = GetScopeValue(value, ctxMap)
			} else {
				if _, exists := current[part]; !exists {
					current[part] = map[string]any{}
				}
				current = current[part].(map[string]any)
			}
		}
	}
	return result
}

// GetScopeValue mirrors TS getScopeValue: if value is a dot-delimited string,
// traverse context fields by that path; otherwise return the value as-is.
func GetScopeValue(value any, ctx map[string]any) any {
	str, ok := value.(string)
	if !ok || str == "" {
		return value
	}
	keys := strings.Split(str, ".")
	var current any = ctx
	for _, k := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return value
		}
		v, found := m[k]
		if !found {
			return value
		}
		current = v
	}
	if current == nil {
		return value
	}
	return current
}

// contextToMap converts NTXContext to a flat map[string]any for scope resolution.
// Keys are camelCase to match the TS context field names that scope values reference.
func contextToMap(ctx ntxctx.NTXContext) map[string]any {
	return map[string]any{
		"method":      ctx.Method,
		"source":      ctx.Source,
		"sink":        ctx.Sink,
		"tracingId":   ctx.TracingID,
		"userId":      ctx.UserID,
		"clientId":    ctx.ClientID,
		"workspaceId": ctx.WorkspaceID,
		"config":      kvToAny(ctx.Config),
		"user":        kvToAny(ctx.User),
		"client":      kvToAny(ctx.Client),
		"workspace":   kvToAny(ctx.Workspace),
		"roles":       ctx.Roles,
		"permissions": ctx.Permissions,
		"scope":       ctx.Scope,
		"preference":  kvToAny(ctx.Preference),
	}
}

func kvToAny(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

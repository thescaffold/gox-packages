package auth

import "strings"

// Permission strings are the scaffold's role/permission catalog format, used
// by every app's DefaultPermissions (ntx-apps/*/src/index.ts defaultRolesNPermissions):
//
//	role:action:product:app:resource:instance:scope
//	e.g. member:read:apps:forms:*:*:workspace
//	     admin:*:apps:forms:*:*:workspace
//
// Segments: role (label, ignored when matching), action (create, read, update,
// delete, share or *), product ("apps"), app (the app name), resource
// (an entity within the app, or *), instance ("one", "*"), scope (self,
// workspace, global). A "*" matches any value.

// PermissionAction values the scaffold vocabulary defines. "share" governs
// changing who has access to something.
const (
	ActionCreate = "create"
	ActionRead   = "read"
	ActionUpdate = "update"
	ActionDelete = "delete"
	ActionShare  = "share"
	ActionAny    = "*"
)

// PermissionScope orders breadth: self < workspace < global.
type PermissionScope int

const (
	ScopeNone PermissionScope = iota
	ScopeSelf
	ScopeWorkspace
	ScopeGlobal
)

// Permission is one parsed permission string.
type Permission struct {
	Role     string
	Action   string
	Product  string
	App      string
	Resource string
	Instance string
	Scope    PermissionScope
	Raw      string
}

// ParsePermission parses a 7-segment permission string. Anything malformed
// (wrong segment count, empty segment, unknown scope) is rejected so that a
// bad catalog row can never widen access.
func ParsePermission(s string) (Permission, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 7 {
		return Permission{}, false
	}
	for _, p := range parts {
		if p == "" {
			return Permission{}, false
		}
	}
	var scope PermissionScope
	switch parts[6] {
	case "self":
		scope = ScopeSelf
	case "workspace":
		scope = ScopeWorkspace
	case "global":
		scope = ScopeGlobal
	default:
		return Permission{}, false
	}
	return Permission{
		Role: parts[0], Action: parts[1], Product: parts[2], App: parts[3],
		Resource: parts[4], Instance: parts[5], Scope: scope, Raw: s,
	}, true
}

// PermissionRequirement is what an operation needs.
type PermissionRequirement struct {
	Action   string          // create, read, update, delete, share
	Product  string          // normally "apps"
	App      string          // e.g. "directory"
	Resource string          // e.g. "item"
	Instance string          // "one" or "*"; "" means "*"
	Scope    PermissionScope // breadth the target needs: self if caller owns it, workspace if it is in the caller's workspace, else global
}

// PermissionSet is a caller's parsed permissions.
type PermissionSet []Permission

// ParsePermissions parses the claim value as found on NTXContext.Permissions
// ([]any of strings) or a []string. Malformed entries are skipped.
func ParsePermissions(v any) PermissionSet {
	var out PermissionSet
	add := func(s string) {
		if p, ok := ParsePermission(s); ok {
			out = append(out, p)
		}
	}
	switch t := v.(type) {
	case []string:
		for _, s := range t {
			add(s)
		}
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				add(s)
			}
		}
	}
	return out
}

func segMatch(held, need string) bool {
	return held == "*" || held == need
}

// Allows reports whether any held permission satisfies req. A held "*" in
// action/product/app/resource/instance matches anything; the held scope must
// be at least as broad as the requirement's. Empty set denies.
func (ps PermissionSet) Allows(req PermissionRequirement) bool {
	inst := req.Instance
	if inst == "" {
		inst = "*"
	}
	for _, p := range ps {
		if !segMatch(p.Action, req.Action) || !segMatch(p.Product, req.Product) ||
			!segMatch(p.App, req.App) || !segMatch(p.Resource, req.Resource) {
			continue
		}
		// An "one"-instance grant cannot satisfy a "*" requirement.
		if p.Instance != "*" && p.Instance != inst {
			continue
		}
		if p.Scope >= req.Scope {
			return true
		}
	}
	return false
}

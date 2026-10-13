package tests

import (
	"testing"

	"github.com/thescaffold/gox-packages/libs/core/auth"
)

func TestParsePermission(t *testing.T) {
	good := []string{"member:read:apps:forms:*:*:workspace", "admin:*:apps:forms:*:*:workspace", "guest:create:apps:forms:one:*:self"}
	for _, s := range good {
		if _, ok := auth.ParsePermission(s); !ok {
			t.Errorf("expected %q to parse", s)
		}
	}
	bad := []string{"", "member:*:*:*:*:workspace", "admin:*:*:*:*:*:planet", "a:b:c:d:e:f:", "a:b:c:d:e:f:g:h"}
	for _, s := range bad {
		if _, ok := auth.ParsePermission(s); ok {
			t.Errorf("expected %q to be rejected", s)
		}
	}
}

func TestPermissionSetAllows(t *testing.T) {
	ps := auth.ParsePermissions([]any{
		"member:create:apps:directory:*:*:workspace",
		"member:read:apps:directory:*:*:workspace",
		"member:update:apps:directory:*:*:self",
		"member:share:apps:directory:*:*:self",
		"bogus",
	})
	cases := []struct {
		name string
		req  auth.PermissionRequirement
		want bool
	}{
		{"read in workspace", auth.PermissionRequirement{Action: "read", Product: "apps", App: "directory", Resource: "item", Scope: auth.ScopeWorkspace}, true},
		{"update own", auth.PermissionRequirement{Action: "update", Product: "apps", App: "directory", Resource: "item", Scope: auth.ScopeSelf}, true},
		{"update someone else's", auth.PermissionRequirement{Action: "update", Product: "apps", App: "directory", Resource: "item", Scope: auth.ScopeWorkspace}, false},
		{"delete not held", auth.PermissionRequirement{Action: "delete", Product: "apps", App: "directory", Resource: "item", Scope: auth.ScopeSelf}, false},
		{"other app", auth.PermissionRequirement{Action: "read", Product: "apps", App: "forms", Resource: "item", Scope: auth.ScopeSelf}, false},
		{"share own", auth.PermissionRequirement{Action: "share", Product: "apps", App: "directory", Resource: "folder", Scope: auth.ScopeSelf}, true},
	}
	for _, c := range cases {
		if got := ps.Allows(c.req); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	admin := auth.ParsePermissions([]string{"admin:*:apps:directory:*:*:workspace"})
	if !admin.Allows(auth.PermissionRequirement{Action: "share", Product: "apps", App: "directory", Resource: "item", Scope: auth.ScopeWorkspace}) {
		t.Error("admin wildcard should allow share in workspace")
	}
	if admin.Allows(auth.PermissionRequirement{Action: "share", Product: "apps", App: "directory", Resource: "item", Scope: auth.ScopeGlobal}) {
		t.Error("workspace-scoped admin must not satisfy global")
	}
	var empty auth.PermissionSet
	if empty.Allows(auth.PermissionRequirement{Action: "read", Product: "apps", App: "directory", Resource: "item", Scope: auth.ScopeSelf}) {
		t.Error("empty set must deny")
	}
}

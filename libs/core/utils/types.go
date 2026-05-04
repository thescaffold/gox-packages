package utils

// KeyValue is the generic map type used throughout the platform.
type KeyValue = map[string]any

// EntityActionType enumerates CRUD operations emitted as events.
type EntityActionType string

const (
	ActionCreate EntityActionType = "create"
	ActionRead   EntityActionType = "read"
	ActionUpdate EntityActionType = "update"
	ActionDelete EntityActionType = "delete"
)

// DefaultRoleTypeName holds the platform default role identifiers.
type DefaultRoleTypeName string

const (
	RoleSuperAdmin DefaultRoleTypeName = "super-admin"
	RoleAdmin      DefaultRoleTypeName = "admin"
	RoleUser       DefaultRoleTypeName = "user"
	RoleGuest      DefaultRoleTypeName = "guest"
)

// PermissionScopeType restricts which records a permission applies to.
type PermissionScopeType string

const (
	ScopeSelf      PermissionScopeType = "self"
	ScopeWorkspace PermissionScopeType = "workspace"
	ScopeGlobal    PermissionScopeType = "global"
)

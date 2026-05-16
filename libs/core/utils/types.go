package utils

// KeyValue is the generic map type used throughout the platform.
type KeyValue = map[string]any

// EntityActionType enumerates CRUD operations emitted as events.
// Mirrors TS EntityActionType (common.util.ts) exactly, including the
// granular read_one / read_many read actions.
type EntityActionType string

const (
	ActionCreate   EntityActionType = "create"
	ActionRead     EntityActionType = "read"
	ActionReadOne  EntityActionType = "read_one"
	ActionReadMany EntityActionType = "read_many"
	ActionUpdate   EntityActionType = "update"
	ActionDelete   EntityActionType = "delete"
)

// DefaultRoleTypeName holds the platform default role identifiers.
// Mirrors TS DefaultRoleTypeName (common.util.ts): global_admin / global_member
// are platform-wide roles, admin / member are workspace-scoped, guest is the
// fallback.
type DefaultRoleTypeName string

const (
	RoleGlobalAdmin  DefaultRoleTypeName = "global_admin"
	RoleGlobalMember DefaultRoleTypeName = "global_member"
	RoleAdmin        DefaultRoleTypeName = "admin"  // workspace
	RoleMember       DefaultRoleTypeName = "member" // workspace
	RoleGuest        DefaultRoleTypeName = "guest"
)

// PermissionScopeType restricts which records a permission applies to.
type PermissionScopeType string

const (
	ScopeSelf      PermissionScopeType = "self"
	ScopeWorkspace PermissionScopeType = "workspace"
	ScopeGlobal    PermissionScopeType = "global"
)

// REQUEST_ENTITY_MANAGER is the DI token for the per-request EntityManager.
// Mirrors the TS constants.util.ts export.
const REQUEST_ENTITY_MANAGER = "REQUEST_ENTITY_MANAGER"

// PeriodType enumerates billing/reporting periods.
type PeriodType string

const (
	PeriodDaily   PeriodType = "daily"
	PeriodWeekly  PeriodType = "weekly"
	PeriodMonthly PeriodType = "monthly"
	PeriodYearly  PeriodType = "yearly"
)

// CommentEntityType enumerates entities that can be commented on.
type CommentEntityType string

const (
	CommentMedia CommentEntityType = "media"
)

// ActionEntityType enumerates entities that can be acted upon.
type ActionEntityType string

const (
	ActionEntityMedia        ActionEntityType = "media"
	ActionEntityComment      ActionEntityType = "comment"
	ActionEntityChat         ActionEntityType = "chat"
	ActionEntityNotification ActionEntityType = "notification"
)

// ActionType enumerates user actions on entities.
type ActionType string

const (
	ActionLike     ActionType = "like"
	ActionShare    ActionType = "share"
	ActionBookmark ActionType = "bookmark"
	ActionSubmit   ActionType = "submit"
	ActionListen   ActionType = "listen"
	ActionSeen     ActionType = "seen"
)

// EarningStatusType enumerates earning lifecycle statuses.
type EarningStatusType string

const (
	EarningPending   EarningStatusType = "pending"
	EarningProcessed EarningStatusType = "processed"
)

// GenderType enumerates user gender values.
type GenderType string

const (
	GenderFemale GenderType = "female"
	GenderMale   GenderType = "male"
)

// UserDocumentType enumerates accepted KYC documents.
type UserDocumentType string

const (
	UserDocDriver   UserDocumentType = "driver"
	UserDocPassport UserDocumentType = "passport"
)

// EventEntityType enumerates polylog event entity classifications.
type EventEntityType string

const (
	EventEntitySource  EventEntityType = "source"
	EventEntityChannel EventEntityType = "channel"
)

// CategoryType enumerates polylog item categories.
type CategoryType string

const (
	CategoryWebhook CategoryType = "webhook"
	CategoryJob     CategoryType = "job"
	CategoryEvent   CategoryType = "event"
	CategoryLog     CategoryType = "log"
	CategoryMessage CategoryType = "message"
	CategoryOthers  CategoryType = "others"
)

// LogType filters log levels.
type LogType string

const (
	LogInfo  LogType = "info"
	LogWarn  LogType = "warn"
	LogError LogType = "error"
)

// Item is the generic platform-wide event/log envelope.
// Mirrors TS Item interface in constants.util.ts.
type Item struct {
	Category   CategoryType    `json:"category"`
	Payload    any             `json:"payload"`
	EntityID   string          `json:"entityId,omitempty"`
	EntityName EventEntityType `json:"entityName,omitempty"`
	Reference  string          `json:"reference,omitempty"`
	Version    string          `json:"version,omitempty"`
	SourceID   string          `json:"sourceId,omitempty"`
	Source     string          `json:"source,omitempty"`
	Type       string          `json:"type,omitempty"`
}

// Items mirrors TS Items wrapper (for batch APIs).
type Items struct {
	Items []Item `json:"items"`
}

// Message is the generic gateway message envelope.
type Message struct {
	Method  string         `json:"method"`
	Headers MessageHeaders `json:"headers,omitempty"`
	Source  string         `json:"source"`
	Sink    string         `json:"sink"`
	Body    any            `json:"body,omitempty"`
	Queries any            `json:"queries,omitempty"`
}

// MessageHeaders carries workspace and config metadata on Message envelopes.
type MessageHeaders struct {
	Workspace KeyValue       `json:"workspace,omitempty"`
	Config    *RequestConfig `json:"config,omitempty"`
	Extra     KeyValue       `json:"-"`
}

// RequestConfig mirrors TS RequestConfigDto.
type RequestConfig struct {
	Log   []string `json:"log,omitempty"`
	Cache *int     `json:"cache,omitempty"`
	Retry *int     `json:"retry,omitempty"`
	Async *bool    `json:"async,omitempty"`
}

// AppDataContract mirrors TS AppDataContract — the standard root app metadata.
type AppDataContract struct {
	Name     string  `json:"name"`
	Desc     string  `json:"desc"`
	URL      string  `json:"url"`
	Business AppMeta `json:"business"`
	Group    AppMeta `json:"group"`
	App      AppMeta `json:"app"`
}

// AppMeta is the nested {name, desc, url, ...} block used by AppDataContract.
type AppMeta struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
	URL  string `json:"url"`
}

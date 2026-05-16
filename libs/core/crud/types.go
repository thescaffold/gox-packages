package crud

import (
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/utils"
)

// Hook names — match TS CrudActionType exactly.
const (
	BeforeCreate = "beforeCreate"
	AfterCreate  = "afterCreate"
	BeforeUpdate = "beforeUpdate"
	AfterUpdate  = "afterUpdate"
	BeforeList   = "beforeList"
	AfterList    = "afterList"
	BeforeGet    = "beforeGet"
	AfterGet     = "afterGet"
	BeforeDelete = "beforeDelete"
	AfterDelete  = "afterDelete"
)

// HookFn is called for side effects (logging, event publishing, etc.).
// payload is the entity or DTO relevant at that lifecycle point.
type HookFn func(payload any, ctx ntxctx.NTXContext) error

// MorphFn transforms payload and returns the modified value.
type MorphFn func(payload any, ctx ntxctx.NTXContext) (any, error)

// HookEvent carries the rich lifecycle context to HookCtxFn — the URL :id,
// the original DTO body, and the resolved entity. Mirrors TS's per-hook
// `(entity, context, payload)` triple. Non-breaking: existing callers keep
// using Hooks; new callers opt into the richer context via HooksCtx.
//
// Field semantics by stage:
//
//	BeforeCreate / BeforeUpdate / BeforeDelete:
//	  - ID:     URL :id (empty for create)
//	  - DTO:    the *C / *U body the controller received
//	  - Entity: the existing row when resolved (nil for create)
//
//	AfterCreate / AfterUpdate:
//	  - ID:     URL :id (empty for create — entity has the new id)
//	  - DTO:    the original *C / *U body (preserves fields not on Entity)
//	  - Entity: the persisted row
//
//	AfterDelete:
//	  - ID:     URL :id
//	  - DTO:    nil (delete has no body)
//	  - Entity: the soft-deleted row (read just before deletion)
//
//	BeforeGet / AfterGet / BeforeList / AfterList:
//	  - ID:     URL :id (single-row paths only)
//	  - DTO:    filter query map for list paths, nil otherwise
//	  - Entity: the resolved row (single) or slice (list)
type HookEvent struct {
	ID     string
	DTO    any
	Entity any
}

// HookCtxFn mirrors HookFn but receives a HookEvent with full lifecycle
// context. Non-breaking — added alongside HookFn so existing controllers
// don't have to migrate.
type HookCtxFn func(event HookEvent, ctx ntxctx.NTXContext) error

// MorphCtxFn mirrors MorphFn but receives a HookEvent. Allows transforms
// that depend on the URL :id or the original DTO.
type MorphCtxFn func(event HookEvent, ctx ntxctx.NTXContext) (any, error)

// Config parameterises a CrudResource for a specific entity type.
type Config[E, C, U any] struct {
	// Name is the human-readable entity name used in response messages (e.g. "User", "List").
	Name string

	// Searchable lists column names that the `query=` param will LIKE-search.
	Searchable []string

	// Unique returns a slice of field maps that must not already exist before create/update.
	// Each map is ANDed; multiple maps are checked independently (any match = conflict).
	// nil means no uniqueness checks. Called on POST / (Create), POST /upsert, and
	// PUT /:id (UpdatePut) — i.e. every path that receives *C.
	Unique func(*C) []utils.KeyValue

	// UniqueU mirrors Unique for the PATCH /:id (Update) path, where the handler
	// receives *U rather than *C. Optional — when nil, PATCH skips the unique-check
	// (matching pre-Phase-1 gox behaviour). TS calls the same `unique(payload)` on
	// both create and updatePatch; libs that share C and U typically only set Unique.
	UniqueU func(*U) []utils.KeyValue

	// Relations exposes named hydrators for FindRelatives (GET /:id/:relative).
	// Map key is the URL :relative segment; the hydrator receives the fetched
	// entity and returns whatever should be sent as the response data (typically
	// the related rows). When nil or the key is absent, FindRelatives falls back
	// to returning the parent entity itself (pre-Phase-1 behaviour).
	Relations map[string]func(entity *E) (any, error)

	// Hooks are side-effect callbacks keyed by one of the Before*/After* constants.
	Hooks map[string]HookFn

	// HooksCtx is the rich-context variant of Hooks — receives a HookEvent with
	// the URL :id + original DTO + resolved entity. When both Hooks[k] and
	// HooksCtx[k] are set, both run (Hooks first); a non-nil return from either
	// short-circuits the request.
	HooksCtx map[string]HookCtxFn

	// Morphs are transformation callbacks keyed by one of the Before*/After* constants.
	// The return value replaces the current payload.
	Morphs map[string]MorphFn

	// MorphsCtx is the rich-context variant of Morphs — receives a HookEvent.
	// When both Morphs[k] and MorphsCtx[k] are set, Morphs runs first and its
	// result is fed to MorphsCtx via HookEvent.Entity / HookEvent.DTO.
	MorphsCtx map[string]MorphCtxFn
}

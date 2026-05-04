package crud

import (
	ntxctx "github.com/thescaffold/gox-packages-core/context"
	"github.com/thescaffold/gox-packages-core/utils"
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

// Config parameterises a CrudResource for a specific entity type.
type Config[E, C, U any] struct {
	// Name is the human-readable entity name used in response messages (e.g. "User", "List").
	Name string

	// Searchable lists column names that the `query=` param will LIKE-search.
	Searchable []string

	// Unique returns a slice of field maps that must not already exist before create/update.
	// Each map is ANDed; multiple maps are checked independently (any match = conflict).
	// nil means no uniqueness checks.
	Unique func(*C) []utils.KeyValue

	// Hooks are side-effect callbacks keyed by one of the Before*/After* constants.
	Hooks map[string]HookFn

	// Morphs are transformation callbacks keyed by one of the Before*/After* constants.
	// The return value replaces the current payload.
	Morphs map[string]MorphFn
}

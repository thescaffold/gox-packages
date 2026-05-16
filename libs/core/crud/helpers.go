package crud

import (
	"sort"
	"strings"

	"github.com/awesome-goose/goose/types"
)

// Generic helpers over types.Entity[T] that mirror the TS service-layer
// patterns used by gox-apps deferred work — `service.findOrCreate(match,
// defaults)`, `service.updateOrCreate(match, payload)`, `service.softDelete(
// where)`, `service.count(where)`. They compose over existing Entity methods
// (Exists, First, Insert, Update, Delete) without forking goose.
//
// All helpers take a goose `types.Entity[T]` so they work uniformly against
// the concrete `sql.Entity[T]` injected into a Service, regardless of which
// app provides the entity wiring.

// FindOrCreate returns the row matching `match`. When no row exists it
// inserts a new row using `defaults` and returns it. The boolean return
// is true when the row was newly created. Mirrors TS findOrCreate(match,
// defaults).
//
// `defaults` must already have its `match` fields populated — the helper
// does not merge `match` into `defaults`. Callers that want TS-style merge
// behaviour can build `defaults` from `match` themselves.
func FindOrCreate[T any](e types.Entity[T], match map[string]any, defaults *T) (*T, bool, error) {
	where, args := MapToWhere(match)
	existing, err := e.First(where, args...)
	if err == nil && existing != nil {
		return existing, false, nil
	}
	if defaults == nil {
		defaults = new(T)
	}
	if err := e.Insert(defaults); err != nil {
		return nil, false, err
	}
	return defaults, true, nil
}

// UpdateOrCreate updates the row matching `match` with `payload`. When no
// row exists it inserts `payload`. The boolean return is true when the row
// was newly created. Mirrors TS updateOrCreate(match, payload).
//
// On update the row is matched by the `match` map (same WHERE used for the
// initial lookup), then re-read so the returned value reflects the post-
// update state.
func UpdateOrCreate[T any](e types.Entity[T], match map[string]any, payload *T) (*T, bool, error) {
	where, args := MapToWhere(match)
	existing, _ := e.First(where, args...)
	if existing == nil {
		if payload == nil {
			payload = new(T)
		}
		if err := e.Insert(payload); err != nil {
			return nil, false, err
		}
		return payload, true, nil
	}
	if _, err := e.Update(payload, where, args...); err != nil {
		return existing, false, err
	}
	updated, err := e.First(where, args...)
	if err != nil || updated == nil {
		return existing, false, err
	}
	return updated, false, nil
}

// SoftDelete removes rows matching the supplied SQL predicate. goose's
// `BaseEntity` embeds GORM's `DeletedAt`, so `Delete` is already a soft
// delete — this helper exists for naming parity with TS softDelete() at
// call sites. Returns the number of rows affected.
func SoftDelete[T any](e types.Entity[T], where string, args ...any) (int64, error) {
	return e.Delete(where, args...)
}

// Count is a thin wrapper over types.Entity[T].Count for naming symmetry
// with the other helpers; callers can use it as a drop-in for TS
// `service.count(filter)`.
func Count[T any](e types.Entity[T], where string, args ...any) (int64, error) {
	return e.Count(where, args...)
}

// MapToWhere converts a {"col": value, ...} filter map into the SQL
// `"col = ? AND col = ?"`, [val, val] pair that goose expects. Keys are
// sorted for deterministic query strings. Promoted from the previously
// private `mapToWhere` in resource.go so helper callers in other packages
// can share the same shape.
func MapToWhere(match map[string]any) (string, []any) {
	if len(match) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(match))
	for k := range match {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	clauses := make([]string, len(keys))
	args := make([]any, len(keys))
	for i, k := range keys {
		clauses[i] = k + " = ?"
		args[i] = match[k]
	}
	return strings.Join(clauses, " AND "), args
}

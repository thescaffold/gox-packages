package crud

import (
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
)

// ListDto is the input for List and FindByIds handlers.
// Queries holds all raw query params (populated by QueriesMiddleware via context:"queries").
// Individual named fields are kept for clarity but the full map drives MakeFilter.
type ListDto struct {
	Queries map[string]string `context:"queries"`
	Ctx     ntxctx.NTXContext `context:"ntx"`
}

// GetDto is the input for Get and FindRelatives handlers.
type GetDto struct {
	ID       string            `param:"id"`
	Relative string            `param:"relative"`
	Ctx      ntxctx.NTXContext `context:"ntx"`
}

// CreateDto[C] merges the JSON request body into Body and injects NTXContext.
// The `json:",merge"` tag tells goose's input binder to spread the entire
// JSON body into Body rather than looking for a nested "body" key.
type CreateDto[C any] struct {
	Body C                 `json:",merge"`
	Ctx  ntxctx.NTXContext `context:"ntx"`
}

// UpdateDto[U] carries the path :id param plus a merged JSON body.
type UpdateDto[U any] struct {
	ID   string            `param:"id"`
	Body U                 `json:",merge"`
	Ctx  ntxctx.NTXContext `context:"ntx"`
}

// UpdatePutDto[C] carries the path :id param plus a merged JSON body of the
// CREATE shape. PUT /:id replaces the row, mirroring TS updatePut() which
// receives a CreateDto plus :id. The :id is required to scope the update to
// a single row — without it, gox previously updated every row via
// "id IS NOT NULL". Phase 1 closes that gap.
type UpdatePutDto[C any] struct {
	ID   string            `param:"id"`
	Body C                 `json:",merge"`
	Ctx  ntxctx.NTXContext `context:"ntx"`
}

// UpsertDto[C] is like CreateDto with an additional ?update= query flag.
type UpsertDto[C any] struct {
	Body   C                 `json:",merge"`
	Update string            `query:"update"`
	Ctx    ntxctx.NTXContext `context:"ntx"`
}

// DeleteDto carries only the :id param.
type DeleteDto struct {
	ID  string            `param:"id"`
	Ctx ntxctx.NTXContext `context:"ntx"`
}

// FindByTypeDto carries the :type path param (latest / oldest).
type FindByTypeDto struct {
	Type    string            `param:"type"`
	Queries map[string]string `context:"queries"`
	Ctx     ntxctx.NTXContext `context:"ntx"`
}

// FindByIdsDto accepts a JSON body with an "ids" array.
type FindByIdsDto struct {
	IDs     []string          `json:"ids"`
	Queries map[string]string `context:"queries"`
	Ctx     ntxctx.NTXContext `context:"ntx"`
}

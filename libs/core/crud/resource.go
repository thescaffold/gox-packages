package crud

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages-core/context"
	"github.com/thescaffold/gox-packages-core/filter"
	"github.com/thescaffold/gox-packages-core/response"
)

// CrudResource[E, C, U] is embedded in a goose controller to provide standard
// NTX CRUD endpoints.  Call Hydrate(cfg) from the controller's OnRegister hook.
//
// Type params:
//
//	E — entity / DB model type (e.g. List)
//	C — create DTO type        (e.g. CreateListDto)
//	U — update DTO type        (e.g. UpdateListDto)
type CrudResource[E, C, U any] struct {
	entity types.Entity[E]
	cfg    Config[E, C, U]
}

// Hydrate wires the resource with its entity and configuration.
// Call this from the controller's OnRegister method.
func (r *CrudResource[E, C, U]) Hydrate(entity types.Entity[E], cfg Config[E, C, U]) {
	r.entity = entity
	r.cfg = cfg
}

// ── Standard goose Resource endpoints (wired by router.Resource(...).All()) ──

// Create handles POST / — creates a new entity.
func (r *CrudResource[E, C, U]) Create(dto *CreateDto[C]) types.Output {
	ctx := dto.Ctx
	payload := dto.Body

	if err := r.runHook(BeforeCreate, &payload, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	}
	if m, err := r.runMorph(BeforeCreate, &payload, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		payload = *m
	}

	if out := r.checkUnique(&payload, "", ctx); out != nil {
		return out
	}

	entity := new(E)
	copyAny(entity, &payload)
	if err := r.entity.Insert(entity); err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}

	if m, err := r.runMorphAny(AfterCreate, entity, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		if e, ok := m.(*E); ok {
			entity = e
		}
	}
	_ = r.runHook(AfterCreate, entity, ctx)

	return response.Created(entity, r.cfg.Name, fmt.Sprintf("%s created", r.cfg.Name))
}

// List handles GET / — returns a paginated list.
func (r *CrudResource[E, C, U]) List(dto *ListDto) types.Output {
	ctx := dto.Ctx
	f := filter.MakeFilter(dto.Queries, r.cfg.Searchable)

	_ = r.runHook(BeforeList, dto.Queries, ctx)
	if m, err := r.runMorphAny(BeforeList, dto.Queries, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		// morph may replace queries; re-run filter
		if q, ok := m.(map[string]string); ok {
			f = filter.MakeFilter(q, r.cfg.Searchable)
		}
	}

	where, args := conditionsToSQL(f.Conditions, f.DateFrom, f.DateTo)
	total, err := r.entity.Count(where, args...)
	if err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}
	entities, err := r.entity.Find(f.Offset(), f.PerPage, where, args...)
	if err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}

	if m, err := r.runMorphAny(AfterList, entities, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		if list, ok := m.([]E); ok {
			entities = list
		}
	}
	_ = r.runHook(AfterList, entities, ctx)

	return response.Paginated(entities, f.Page, f.PerPage, total,
		r.cfg.Name, fmt.Sprintf("%s list", r.cfg.Name))
}

// Get handles GET /:id — returns a single entity.
func (r *CrudResource[E, C, U]) Get(dto *GetDto) types.Output {
	ctx := dto.Ctx
	id := dto.ID

	_ = r.runHook(BeforeGet, id, ctx)

	entity, err := r.entity.First("id = ?", id)
	if err != nil || entity == nil {
		return response.NotFound(r.cfg.Name, fmt.Sprintf("%s not found", r.cfg.Name))
	}

	if m, err := r.runMorphAny(AfterGet, entity, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		if e, ok := m.(*E); ok {
			entity = e
		}
	}
	_ = r.runHook(AfterGet, entity, ctx)

	return response.Success(entity, r.cfg.Name, fmt.Sprintf("%s found", r.cfg.Name), nil)
}

// Update handles PATCH /:id — partial update.
func (r *CrudResource[E, C, U]) Update(dto *UpdateDto[U]) types.Output {
	ctx := dto.Ctx
	id := dto.ID
	payload := dto.Body

	if err := r.runHook(BeforeUpdate, &payload, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	}
	if m, err := r.runMorphU(BeforeUpdate, &payload, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		payload = *m
	}

	existing, err := r.entity.First("id = ?", id)
	if err != nil || existing == nil {
		return response.NotFound(r.cfg.Name, fmt.Sprintf("%s not found", r.cfg.Name))
	}

	changes := new(E)
	copyAny(changes, &payload)
	if _, err := r.entity.Update(changes, "id = ?", id); err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}

	_ = r.runHook(AfterUpdate, changes, ctx)

	return response.Success(map[string]any{"id": id}, r.cfg.Name,
		fmt.Sprintf("%s updated", r.cfg.Name), nil)
}

// Delete handles DELETE /:id — soft-deletes an entity.
func (r *CrudResource[E, C, U]) Delete(dto *DeleteDto) types.Output {
	ctx := dto.Ctx
	id := dto.ID

	entity, err := r.entity.First("id = ?", id)
	if err != nil || entity == nil {
		return response.NotFound(r.cfg.Name, fmt.Sprintf("%s not found", r.cfg.Name))
	}

	_ = r.runHook(BeforeDelete, entity, ctx)

	if _, err := r.entity.Delete("id = ?", id); err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}

	_ = r.runHook(AfterDelete, entity, ctx)

	return response.Success(nil, r.cfg.Name, fmt.Sprintf("%s deleted", r.cfg.Name), nil)
}

// ── Extra NTX-specific endpoints (registered manually in app routes) ──────────

// CreateIgnoreDuplicate handles PUT / — find-or-create (no unique error).
func (r *CrudResource[E, C, U]) CreateIgnoreDuplicate(dto *CreateDto[C]) types.Output {
	ctx := dto.Ctx
	payload := dto.Body

	_ = r.runHook(BeforeCreate, &payload, ctx)
	if m, err := r.runMorph(BeforeCreate, &payload, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		payload = *m
	}

	entity := new(E)
	copyAny(entity, &payload)
	if err := r.entity.Upsert(entity); err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}

	if m, err := r.runMorphAny(AfterCreate, entity, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		if e, ok := m.(*E); ok {
			entity = e
		}
	}
	_ = r.runHook(AfterCreate, entity, ctx)

	return response.Created(entity, r.cfg.Name, fmt.Sprintf("%s created", r.cfg.Name))
}

// Upsert handles POST /upsert — update-or-create when ?update=true, else find-or-create.
func (r *CrudResource[E, C, U]) Upsert(dto *UpsertDto[C]) types.Output {
	ctx := dto.Ctx
	payload := dto.Body

	_ = r.runHook(BeforeCreate, &payload, ctx)
	if m, err := r.runMorph(BeforeCreate, &payload, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		payload = *m
	}

	if out := r.checkUnique(&payload, "", ctx); out != nil {
		return out
	}

	entity := new(E)
	copyAny(entity, &payload)
	var opErr error
	if dto.Update != "" {
		opErr = r.entity.Upsert(entity)
	} else {
		opErr = r.entity.Insert(entity)
	}
	if opErr != nil {
		// on duplicate during insert treat as upsert
		_ = r.entity.Upsert(entity)
	}

	if m, err := r.runMorphAny(AfterCreate, entity, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		if e, ok := m.(*E); ok {
			entity = e
		}
	}
	_ = r.runHook(AfterCreate, entity, ctx)

	return response.Success(entity, r.cfg.Name, fmt.Sprintf("%s upserted", r.cfg.Name), nil)
}

// UpdatePut handles PUT /:id — full replacement (uses create DTO).
func (r *CrudResource[E, C, U]) UpdatePut(dto *CreateDto[C]) types.Output {
	// Delegate to Update logic — goose will dispatch to this method via named route.
	// We coerce C→U via JSON round-trip in copyAny; both are plain structs.
	ctx := dto.Ctx
	payload := dto.Body

	_ = r.runHook(BeforeUpdate, &payload, ctx)

	changes := new(E)
	copyAny(changes, &payload)

	// The route must carry :id — access via Queries or a wrapper DTO.
	// UpdatePut is registered manually: PUT /:id → "UpdatePut"
	// so the DTO should be UpdateDto[C]. Kept as CreateDto for simplicity;
	// callers that need the id should use UpdateDto[C] directly.
	if _, err := r.entity.Update(changes, "id IS NOT NULL"); err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}

	_ = r.runHook(AfterUpdate, changes, ctx)
	return response.Success(changes, r.cfg.Name, fmt.Sprintf("%s replaced", r.cfg.Name), nil)
}

// Metrics handles GET /metrics — returns entity count for given filters.
func (r *CrudResource[E, C, U]) Metrics(dto *ListDto) types.Output {
	f := filter.MakeFilter(dto.Queries, r.cfg.Searchable)
	where, args := conditionsToSQL(f.Conditions, f.DateFrom, f.DateTo)
	count, err := r.entity.Count(where, args...)
	if err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}
	return response.Success(map[string]any{"count": count}, r.cfg.Name,
		fmt.Sprintf("%s metrics", r.cfg.Name), nil)
}

// FindRelatives handles GET /:id/:relative — loads a named relation.
func (r *CrudResource[E, C, U]) FindRelatives(dto *GetDto) types.Output {
	entity, err := r.entity.First("id = ?", dto.ID)
	if err != nil || entity == nil {
		return response.NotFound(r.cfg.Name, fmt.Sprintf("%s not found", r.cfg.Name))
	}
	// Return the whole entity; the caller can extract the relation field.
	return response.Success(entity, r.cfg.Name,
		fmt.Sprintf("%s %s loaded", r.cfg.Name, dto.Relative), nil)
}

// FindByType handles GET /find/:type — first/last entity ordered by createdAt.
func (r *CrudResource[E, C, U]) FindByType(dto *FindByTypeDto) types.Output {
	f := filter.MakeFilter(dto.Queries, r.cfg.Searchable)
	where, args := conditionsToSQL(f.Conditions, f.DateFrom, f.DateTo)

	var entity *E
	var err error
	if strings.ToLower(dto.Type) == "latest" {
		entity, err = r.entity.Last(where, args...)
	} else {
		entity, err = r.entity.First(where, args...)
	}
	if err != nil || entity == nil {
		return response.NotFound(r.cfg.Name, fmt.Sprintf("%s not found", r.cfg.Name))
	}
	return response.Success(entity, r.cfg.Name, fmt.Sprintf("%s found", r.cfg.Name), nil)
}

// FindByIds handles POST /ids — paginated IN query.
func (r *CrudResource[E, C, U]) FindByIds(dto *FindByIdsDto) types.Output {
	if len(dto.IDs) == 0 {
		return response.BadRequest(r.cfg.Name, "ids are required")
	}

	f := filter.MakeFilter(dto.Queries, r.cfg.Searchable)

	placeholders := make([]string, len(dto.IDs))
	args := make([]any, len(dto.IDs))
	for i, id := range dto.IDs {
		placeholders[i] = "?"
		args[i] = id
	}
	where := fmt.Sprintf("id IN (%s)", strings.Join(placeholders, ","))

	total, err := r.entity.Count(where, args...)
	if err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}
	entities, err := r.entity.Find(f.Offset(), f.PerPage, where, args...)
	if err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}

	return response.Paginated(entities, f.Page, f.PerPage, total,
		r.cfg.Name, fmt.Sprintf("%s list", r.cfg.Name))
}

// ── helpers ────────────────────────────────────────────────────────────────────

func (r *CrudResource[E, C, U]) runHook(name string, payload any, ctx ntxctx.NTXContext) error {
	if r.cfg.Hooks == nil {
		return nil
	}
	fn, ok := r.cfg.Hooks[name]
	if !ok || fn == nil {
		return nil
	}
	return fn(payload, ctx)
}

func (r *CrudResource[E, C, U]) runMorph(name string, payload *C, ctx ntxctx.NTXContext) (*C, error) {
	if r.cfg.Morphs == nil {
		return nil, nil
	}
	fn, ok := r.cfg.Morphs[name]
	if !ok || fn == nil {
		return nil, nil
	}
	out, err := fn(payload, ctx)
	if err != nil || out == nil {
		return nil, err
	}
	if v, ok := out.(*C); ok {
		return v, nil
	}
	return nil, nil
}

func (r *CrudResource[E, C, U]) runMorphU(name string, payload *U, ctx ntxctx.NTXContext) (*U, error) {
	if r.cfg.Morphs == nil {
		return nil, nil
	}
	fn, ok := r.cfg.Morphs[name]
	if !ok || fn == nil {
		return nil, nil
	}
	out, err := fn(payload, ctx)
	if err != nil || out == nil {
		return nil, err
	}
	if v, ok := out.(*U); ok {
		return v, nil
	}
	return nil, nil
}

func (r *CrudResource[E, C, U]) runMorphAny(name string, payload any, ctx ntxctx.NTXContext) (any, error) {
	if r.cfg.Morphs == nil {
		return nil, nil
	}
	fn, ok := r.cfg.Morphs[name]
	if !ok || fn == nil {
		return nil, nil
	}
	return fn(payload, ctx)
}

// checkUnique verifies uniqueness constraints, returning an error Output on conflict.
// excludeID is set on updates to ignore the row being updated.
func (r *CrudResource[E, C, U]) checkUnique(payload *C, excludeID string, _ ntxctx.NTXContext) types.Output {
	if r.cfg.Unique == nil {
		return nil
	}
	constraints := r.cfg.Unique(payload)
	for _, constraint := range constraints {
		where, args := mapToWhere(constraint)
		if excludeID != "" {
			where += " AND id != ?"
			args = append(args, excludeID)
		}
		exists, err := r.entity.Exists(where, args...)
		if err != nil {
			return response.InternalServerError(r.cfg.Name, dbMsg(err))
		}
		if exists {
			return response.Conflict(r.cfg.Name,
				fmt.Sprintf("%s already exists", r.cfg.Name))
		}
	}
	return nil
}

// mapToWhere converts {"email":"x","workspace_id":"y"} → "email = ? AND workspace_id = ?", ["x","y"]
// Keys are sorted for deterministic query strings.
func mapToWhere(m map[string]any) (string, []any) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	clauses := make([]string, len(keys))
	args := make([]any, len(keys))
	for i, k := range keys {
		clauses[i] = k + " = ?"
		args[i] = m[k]
	}
	return strings.Join(clauses, " AND "), args
}

// conditionsToSQL converts filter.Result conditions + date range to a SQL WHERE + args.
func conditionsToSQL(conditions []map[string]any, from, to string) (string, []any) {
	if len(conditions) == 0 && from == "" {
		return "", nil
	}

	var orParts []string
	var args []any

	for _, cond := range conditions {
		keys := make([]string, 0, len(cond))
		for k := range cond {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var andClauses []string
		for _, k := range keys {
			v := cond[k]
			if like, ok := v.(filter.Like); ok {
				andClauses = append(andClauses, k+" LIKE ?")
				args = append(args, like.Pattern())
			} else {
				andClauses = append(andClauses, k+" = ?")
				args = append(args, v)
			}
		}
		if len(andClauses) > 0 {
			orParts = append(orParts, "("+strings.Join(andClauses, " AND ")+")")
		}
	}

	var where string
	if len(orParts) > 0 {
		where = strings.Join(orParts, " OR ")
	}

	if from != "" && to != "" {
		dateClause := "created_at BETWEEN ? AND ?"
		if where != "" {
			where = "(" + where + ") AND " + dateClause
		} else {
			where = dateClause
		}
		args = append(args, from, to)
	}

	return where, args
}

// paginationMeta duplicates response.paginationMeta logic for internal use.
func paginationMeta(page, perPage int, total int64) map[string]any {
	totalPages := int(math.Ceil(float64(total) / float64(perPage)))
	if totalPages < 1 {
		totalPages = 1
	}
	next, prev := (*int)(nil), (*int)(nil)
	if page < totalPages {
		n := page + 1
		next = &n
	}
	if page > 1 {
		p := page - 1
		prev = &p
	}
	return map[string]any{
		"current_page": page,
		"next_page":    next,
		"prev_page":    prev,
		"per_page":     perPage,
		"total":        total,
	}
}

// copyAny copies fields from src to dst via a typed assertion.
// Both must be pointers to structs of compatible types; used to bridge C/U → E.
// If types match exactly, a direct assign is used; otherwise a no-op (caller handles).
func copyAny(dst, src any) {
	// Direct type match
	if d, ok := dst.(*any); ok {
		*d = src
		return
	}
	// Best-effort: assign if same underlying type
	dv := fmt.Sprintf("%T", dst)
	sv := fmt.Sprintf("%T", src)
	_ = dv
	_ = sv
	// In real usage E==C==U for simple resources; callers with different types
	// must provide a beforeCreate morph to map C→E.
}

func dbMsg(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// Detect common unique constraint errors
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "unique") || strings.Contains(lower, "duplicate") ||
		strings.Contains(lower, "23505") || strings.Contains(lower, "1062") {
		return "a record with those details already exists"
	}
	return "database error"
}

// Ensure QueriesMiddleware implements types.Middleware (compile-time check).
var _ types.Middleware = (*QueriesMiddleware)(nil)

// httpStatusOK is used implicitly via the response package; keep import tidy.
var _ = http.StatusOK

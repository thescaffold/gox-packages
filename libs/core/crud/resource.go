package crud

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/filter"
	"github.com/thescaffold/gox-packages/libs/core/i18n"
	"github.com/thescaffold/gox-packages/libs/core/response"
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
	lang   *i18n.Service
}

// Hydrate wires the resource with its entity and configuration.
// Call this from the controller's OnRegister method.
func (r *CrudResource[E, C, U]) Hydrate(entity types.Entity[E], cfg Config[E, C, U]) {
	r.entity = entity
	r.cfg = cfg
}

// SetLang wires the translation service used for response titles and messages.
// It mirrors the TS CrudControllerFactory, which resolves every response
// string via this.translate('packages.core.crud.*', { name }, preference).
// It is optional: when unset, keys degrade to their tail path exactly as the
// TS translate() does when no translations are loaded. Call it from the
// controller's OnRegister hook, passing an injected *i18n.Service.
func (r *CrudResource[E, C, U]) SetLang(lang *i18n.Service) { r.lang = lang }

// tr translates a `packages.core.crud.*` key with { name: cfg.Name } data and
// the request preference — mirroring TS this.translate(path, { name }, preference).
// r.lang may be nil; *i18n.Service.Translate is nil-safe and yields the tail key.
func (r *CrudResource[E, C, U]) tr(path string, ctx ntxctx.NTXContext) string {
	return r.lang.Translate(path, map[string]any{"name": r.cfg.Name}, ctx.Preference)
}

// trWith is tr with extra template data merged on top of { name }.
func (r *CrudResource[E, C, U]) trWith(path string, ctx ntxctx.NTXContext, extra map[string]any) string {
	data := map[string]any{"name": r.cfg.Name}
	for k, v := range extra {
		data[k] = v
	}
	return r.lang.Translate(path, data, ctx.Preference)
}

// title is the standard CRUD response title — translate('packages.core.crud.title').
func (r *CrudResource[E, C, U]) title(ctx ntxctx.NTXContext) string {
	return r.tr("packages.core.crud.title", ctx)
}

// ── Standard goose Resource endpoints (wired by router.Resource(...).All()) ──

// Create handles POST / — creates a new entity. Mirrors TS create(): returns
// HTTP 200 (success()), a uniqueness clash returns a 400 error() envelope.
func (r *CrudResource[E, C, U]) Create(dto *CreateDto[C]) types.Output {
	ctx := dto.Ctx
	payload := dto.Body

	if err := r.runHook(BeforeCreate, &payload, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	}
	if err := r.runHookCtx(BeforeCreate, HookEvent{DTO: &payload}, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	}
	if m, err := r.runMorph(BeforeCreate, &payload, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		payload = *m
	}

	if out := r.checkUnique(&payload, "", ctx, "packages.core.crud.post.create.error.existing"); out != nil {
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
	_ = r.runHookCtx(AfterCreate, HookEvent{Entity: entity, DTO: &payload}, ctx)

	return response.Success(entity, r.title(ctx),
		r.tr("packages.core.crud.post.create.success", ctx), nil)
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
		r.title(ctx), r.tr("packages.core.crud.get.all.success", ctx))
}

// Get handles GET /:id — returns a single entity. Mirrors TS findOne():
// a miss returns a 404 error() envelope.
func (r *CrudResource[E, C, U]) Get(dto *GetDto) types.Output {
	ctx := dto.Ctx
	id := dto.ID

	_ = r.runHook(BeforeGet, id, ctx)

	entity, err := r.entity.First("id = ?", id)
	if err != nil || entity == nil {
		return response.NotFound(r.title(ctx),
			r.tr("packages.core.crud.get.one.error.not-found", ctx))
	}

	if m, err := r.runMorphAny(AfterGet, entity, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		if e, ok := m.(*E); ok {
			entity = e
		}
	}
	_ = r.runHook(AfterGet, entity, ctx)

	return response.Success(entity, r.title(ctx),
		r.tr("packages.core.crud.get.one.success", ctx), nil)
}

// Update handles PATCH /:id — partial update. Mirrors TS updatePatch():
// a missing row returns a 400 error() envelope (TS passes no status code to
// error(), so it defaults to BAD_REQUEST — not NOT_FOUND).
func (r *CrudResource[E, C, U]) Update(dto *UpdateDto[U]) types.Output {
	ctx := dto.Ctx
	id := dto.ID
	payload := dto.Body

	if err := r.runHook(BeforeUpdate, &payload, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	}
	if err := r.runHookCtx(BeforeUpdate, HookEvent{ID: id, DTO: &payload}, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	}
	if m, err := r.runMorphU(BeforeUpdate, &payload, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		payload = *m
	}

	existing, err := r.entity.First("id = ?", id)
	if err != nil || existing == nil {
		return response.BadRequest(r.title(ctx),
			r.tr("packages.core.crud.patch.update.error.not-found", ctx))
	}

	// Second BeforeUpdate ctx-pass: now we have the resolved entity so the
	// hook can compare prior vs. requested values (TS-equivalent
	// already-updated guard pattern).
	if err := r.runHookCtx(BeforeUpdate, HookEvent{ID: id, DTO: &payload, Entity: existing}, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	}

	// Mirror TS updatePatch(): unique-check runs on the morphed payload, with
	// the current row excluded so re-saving its own key is not a conflict.
	if out := r.checkUniqueU(&payload, id, ctx, "packages.core.crud.patch.update.error.existing"); out != nil {
		return out
	}

	changes := new(E)
	copyAny(changes, &payload)
	if _, err := r.entity.Update(changes, "id = ?", id); err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}

	_ = r.runHook(AfterUpdate, changes, ctx)
	_ = r.runHookCtx(AfterUpdate, HookEvent{ID: id, DTO: &payload, Entity: changes}, ctx)

	return response.Success(map[string]any{"id": id}, r.title(ctx),
		r.tr("packages.core.crud.patch.update.success", ctx), nil)
}

// Delete handles DELETE /:id — soft-deletes an entity. Mirrors TS remove():
// a miss returns a 404 error() envelope.
func (r *CrudResource[E, C, U]) Delete(dto *DeleteDto) types.Output {
	ctx := dto.Ctx
	id := dto.ID

	entity, err := r.entity.First("id = ?", id)
	if err != nil || entity == nil {
		return response.NotFound(r.title(ctx),
			r.tr("packages.core.crud.delete.remove.error.not-found", ctx))
	}

	if err := r.runHook(BeforeDelete, entity, ctx); err != nil {
		return response.BadRequest(r.title(ctx), err.Error())
	}
	if err := r.runHookCtx(BeforeDelete, HookEvent{ID: id, Entity: entity}, ctx); err != nil {
		return response.BadRequest(r.title(ctx), err.Error())
	}

	if _, err := r.entity.Delete("id = ?", id); err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}

	_ = r.runHook(AfterDelete, entity, ctx)
	_ = r.runHookCtx(AfterDelete, HookEvent{ID: id, Entity: entity}, ctx)

	return response.Success(nil, r.title(ctx),
		r.tr("packages.core.crud.delete.remove.success", ctx), nil)
}

// ── Extra NTX-specific endpoints (registered manually in app routes) ──────────

// CreateIgnoreDuplicate handles PUT / — find-or-create (no unique error).
// Mirrors TS createIgnoreDuplicate(): returns HTTP 200 (success()).
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

	return response.Success(entity, r.title(ctx),
		r.tr("packages.core.crud.put.create.success", ctx), nil)
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

	if out := r.checkUnique(&payload, "", ctx, "packages.core.crud.post.upsert.error.existing"); out != nil {
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

	return response.Success(entity, r.title(ctx),
		r.tr("packages.core.crud.post.upsert.success", ctx), nil)
}

// UpdatePut handles PUT /:id — full replacement using the CREATE shape.
// Mirrors TS updatePut(): scope the update to a single row via :id, run
// Before/After hooks + the BeforeCreate morph (TS uses morphRequest on the
// CreateDto here), and return the resulting entity. A missing :id row mirrors
// the TS error() envelope (BadRequest). Unlike PATCH, the TS PUT unique-check
// does NOT exclude the row being updated (its where clause is `unique` with no
// `id: Not(id)`), so re-submitting a row's own unique value is itself a
// conflict — we mirror that by passing an empty excludeID below.
func (r *CrudResource[E, C, U]) UpdatePut(dto *UpdatePutDto[C]) types.Output {
	ctx := dto.Ctx
	id := dto.ID
	payload := dto.Body

	if err := r.runHook(BeforeUpdate, &payload, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	}
	// TS uses the BeforeCreate morph on the PUT body (because the body is a
	// CreateDto) — gox mirrors that here.
	if m, err := r.runMorph(BeforeCreate, &payload, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		payload = *m
	}

	existing, err := r.entity.First("id = ?", id)
	if err != nil || existing == nil {
		return response.BadRequest(r.title(ctx),
			r.tr("packages.core.crud.put.update.error.not-found", ctx))
	}

	// TS updatePut does not exclude the current row from the unique-check.
	if out := r.checkUnique(&payload, "", ctx, "packages.core.crud.put.update.error.existing"); out != nil {
		return out
	}

	changes := new(E)
	copyAny(changes, &payload)
	if _, err := r.entity.Update(changes, "id = ?", id); err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}

	_ = r.runHook(AfterUpdate, changes, ctx)
	return response.Success(changes, r.title(ctx),
		r.tr("packages.core.crud.put.update.success", ctx), nil)
}

// Metrics handles GET /metrics — returns entity count for given filters.
// Mirrors TS metrics() which returns data { entities: <count> }.
func (r *CrudResource[E, C, U]) Metrics(dto *ListDto) types.Output {
	ctx := dto.Ctx
	f := filter.MakeFilter(dto.Queries, r.cfg.Searchable)
	where, args := conditionsToSQL(f.Conditions, f.DateFrom, f.DateTo)
	count, err := r.entity.Count(where, args...)
	if err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}
	return response.Success(map[string]any{"entities": count}, r.title(ctx),
		r.tr("packages.core.crud.get.metrics.success", ctx), nil)
}

// FindRelatives handles GET /:id/:relative — loads a named relation.
// Mirrors TS findRelatives() which returns `entity[relative]` (the related
// row(s)) rather than the parent entity. gox dispatches via Config.Relations:
// when the :relative key has a registered hydrator, the hydrator's result is
// returned as `data`. When no hydrator is registered, the parent entity is
// returned (matching pre-Phase-1 behaviour) so controllers without relation
// wiring still respond rather than 500.
func (r *CrudResource[E, C, U]) FindRelatives(dto *GetDto) types.Output {
	ctx := dto.Ctx
	entity, err := r.entity.First("id = ?", dto.ID)
	if err != nil || entity == nil {
		return response.NotFound(r.title(ctx),
			r.tr("packages.core.crud.get.relative.error.not-found", ctx))
	}

	var data any = entity
	if r.cfg.Relations != nil {
		if hydrator, ok := r.cfg.Relations[dto.Relative]; ok && hydrator != nil {
			rel, hErr := hydrator(entity)
			if hErr != nil {
				return response.InternalServerError(r.cfg.Name, hErr.Error())
			}
			data = rel
		}
	}

	return response.Success(data, r.title(ctx),
		r.trWith("packages.core.crud.get.relative.success", ctx,
			map[string]any{"relative": dto.Relative}), nil)
}

// FindByType handles GET /find/:type — first/last entity ordered by createdAt.
// Mirrors TS findOneByKeyValue(): a miss returns a 404 error() envelope.
func (r *CrudResource[E, C, U]) FindByType(dto *FindByTypeDto) types.Output {
	ctx := dto.Ctx
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
		return response.NotFound(r.title(ctx),
			r.tr("packages.core.crud.get.find.error.not-found", ctx))
	}
	return response.Success(entity, r.title(ctx),
		r.tr("packages.core.crud.get.find.success", ctx), nil)
}

// FindByIds handles POST /ids — paginated IN query. Mirrors TS findByIds():
// an empty ids array returns a 400 error() envelope.
func (r *CrudResource[E, C, U]) FindByIds(dto *FindByIdsDto) types.Output {
	ctx := dto.Ctx
	if len(dto.IDs) == 0 {
		return response.BadRequest(r.title(ctx),
			r.tr("packages.core.crud.post.create.error.ids-required", ctx))
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
		r.title(ctx), r.tr("packages.core.crud.get.all.success", ctx))
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

// runHookCtx fires the rich-context hook registered under `name`. Returns nil
// when nothing is wired. Both Hooks[name] and HooksCtx[name] may be set; the
// caller invokes runHook first, then runHookCtx as a second pass.
func (r *CrudResource[E, C, U]) runHookCtx(name string, event HookEvent, ctx ntxctx.NTXContext) error {
	if r.cfg.HooksCtx == nil {
		return nil
	}
	fn, ok := r.cfg.HooksCtx[name]
	if !ok || fn == nil {
		return nil
	}
	return fn(event, ctx)
}

// runMorphCtx fires the rich-context morph registered under `name`. Returns
// (nil, nil) when nothing is wired so callers can branch on either error or
// non-nil return.
func (r *CrudResource[E, C, U]) runMorphCtx(name string, event HookEvent, ctx ntxctx.NTXContext) (any, error) {
	if r.cfg.MorphsCtx == nil {
		return nil, nil
	}
	fn, ok := r.cfg.MorphsCtx[name]
	if !ok || fn == nil {
		return nil, nil
	}
	return fn(event, ctx)
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

// checkUnique verifies uniqueness constraints, returning a 400 error Output on
// conflict (mirroring TS error() which defaults to BAD_REQUEST). errKey is the
// `packages.core.crud.*.error.existing` translation key for the calling verb.
// excludeID is set on updates to ignore the row being updated.
func (r *CrudResource[E, C, U]) checkUnique(payload *C, excludeID string, ctx ntxctx.NTXContext, errKey string) types.Output {
	if r.cfg.Unique == nil {
		return nil
	}
	return r.runUniqueChecks(r.cfg.Unique(payload), excludeID, ctx, errKey)
}

// checkUniqueU mirrors checkUnique for the PATCH /:id (Update) path, which
// receives *U. Optional — when Config.UniqueU is nil, the unique-check is
// skipped (libs that share C and U typically only set Unique; libs that
// genuinely differ between create and update DTOs set both).
func (r *CrudResource[E, C, U]) checkUniqueU(payload *U, excludeID string, ctx ntxctx.NTXContext, errKey string) types.Output {
	if r.cfg.UniqueU == nil {
		return nil
	}
	return r.runUniqueChecks(r.cfg.UniqueU(payload), excludeID, ctx, errKey)
}

// runUniqueChecks is the shared body for checkUnique / checkUniqueU.
// Uses the exported MapToWhere (helpers.go) so the same predicate-build logic
// is reused by FindOrCreate / UpdateOrCreate / external callers.
func (r *CrudResource[E, C, U]) runUniqueChecks(constraints []map[string]any, excludeID string, ctx ntxctx.NTXContext, errKey string) types.Output {
	for _, constraint := range constraints {
		where, args := MapToWhere(constraint)
		if excludeID != "" {
			where += " AND id != ?"
			args = append(args, excludeID)
		}
		exists, err := r.entity.Exists(where, args...)
		if err != nil {
			return response.InternalServerError(r.cfg.Name, dbMsg(err))
		}
		if exists {
			return response.BadRequest(r.title(ctx), r.tr(errKey, ctx))
		}
	}
	return nil
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

// copyAny copies fields from src to dst by matching json tag / field name.
// It is used to bridge a CRUD DTO (C or U) onto the persisted entity (E) and
// must populate fields even when the types differ — that is the common case
// once a DTO is anything other than the entity itself.
//
// The implementation uses a JSON round-trip: marshal src, unmarshal into dst.
// Any field on src whose json tag matches a tag on dst is copied; unknown
// fields are silently dropped, mirroring the TS spread-into-save behavior.
// This is the cheap "good enough" approach used by every gox-app CRUD path —
// a reflective field-walker would be marginally faster but adds complexity
// without a behavior win.
func copyAny(dst, src any) {
	if dst == nil || src == nil {
		return
	}
	// Direct *any sink — used by a couple of generic call sites.
	if d, ok := dst.(*any); ok {
		*d = src
		return
	}
	bytes, err := json.Marshal(src)
	if err != nil {
		return
	}
	_ = json.Unmarshal(bytes, dst)
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

// paginationMeta is retained for callers outside this file; reference it so the
// compiler keeps it even when unused locally.
var _ = paginationMeta

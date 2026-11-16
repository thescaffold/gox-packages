package crud

import (
	"encoding/json"
	"fmt"

	gsql "github.com/awesome-goose/goose/modules/sql"
	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/response"
)

// BulkCreateDto is the body of POST /bulk: {"items": [ <create body>, ... ]}.
type BulkCreateDto[C any] struct {
	Items []C               `json:"items"`
	Ctx   ntxctx.NTXContext `context:"ntx"`
}

// BulkUpdateItem is one PATCH /bulk item: the row's id beside the fields to
// change, as in {"id": "…", "name": "…"}.
type BulkUpdateItem[U any] struct {
	ID   string
	Body U
}

// UnmarshalJSON reads "id" and the update fields from one object.
func (i *BulkUpdateItem[U]) UnmarshalJSON(b []byte) error {
	var id struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &id); err != nil {
		return err
	}
	i.ID = id.ID
	return json.Unmarshal(b, &i.Body)
}

// MarshalJSON writes the item back as one object.
func (i BulkUpdateItem[U]) MarshalJSON() ([]byte, error) {
	body, err := json.Marshal(i.Body)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	_ = json.Unmarshal(body, &m)
	m["id"] = i.ID
	return json.Marshal(m)
}

// BulkUpdateDto is the body of PATCH /bulk: {"items": [ {"id": …, …}, … ]}.
type BulkUpdateDto[U any] struct {
	Items []BulkUpdateItem[U] `json:"items"`
	Ctx   ntxctx.NTXContext   `context:"ntx"`
}

// BulkCreated is what BulkCreate answers.
type BulkCreated[E any] struct {
	Items []*E `json:"items"`
}

// BulkUpdated is what BulkUpdate answers.
type BulkUpdated struct {
	IDs []string `json:"ids"`
}

func (r *CrudResource[E, C, U]) bulkLimit() int {
	if r.cfg.BulkLimit > 0 {
		return r.cfg.BulkLimit
	}
	return 100
}

// itemFail is the error for one item of a batch: the item's own status, with
// its message prefixed "item 3: ".
func (r *CrudResource[E, C, U]) itemFail(i int, out types.Output, ctx ntxctx.NTXContext) types.Output {
	msg := r.tr("packages.core.crud.bulk.error.item", ctx)
	if env, ok := out.Data().(response.Envelope); ok && env.Message != "" {
		msg = env.Message
	}
	code := out.Code()
	if code < 400 {
		code = 400
	}
	return response.Error(r.title(ctx), fmt.Sprintf("item %d: %s", i, msg), code)
}

// BulkCreate handles POST /bulk: it creates every item or none. Each item goes
// through Create's pipeline (ValidateBody, BeforeCreate hooks and morphs,
// unique checks, the workspace/owner stamp); the rows are then inserted in
// one statement, and AfterCreate runs for each. The first item that fails
// answers 400 "item <index>: …" and nothing is written. Register it in the
// app's routes: router.Post("/bulk", c.BulkCreate).
func (r *CrudResource[E, C, U]) BulkCreate(dto *BulkCreateDto[C]) types.Output {
	ctx := dto.Ctx
	if out := r.guard(ctx); out != nil {
		return out
	}
	if len(dto.Items) == 0 {
		return response.BadRequest(r.title(ctx), r.tr("packages.core.crud.bulk.error.empty", ctx))
	}
	if len(dto.Items) > r.bulkLimit() {
		return response.BadRequest(r.title(ctx), r.trWith("packages.core.crud.bulk.error.too-many", ctx, map[string]any{"limit": r.bulkLimit()}))
	}
	payloads := make([]C, len(dto.Items))
	rows := make([]*E, len(dto.Items))
	for i := range dto.Items {
		payload := dto.Items[i]
		if out := r.validateBody(&payload, false, ctx); out != nil {
			return r.itemFail(i, out, ctx)
		}
		if err := r.runHook(BeforeCreate, &payload, ctx); err != nil {
			return r.itemFail(i, r.hookFail(err, ctx), ctx)
		}
		if err := r.runHookCtx(BeforeCreate, HookEvent{DTO: &payload}, ctx); err != nil {
			return r.itemFail(i, r.hookFail(err, ctx), ctx)
		}
		if m, err := r.runMorph(BeforeCreate, &payload, ctx); err != nil {
			return r.itemFail(i, r.hookFail(err, ctx), ctx)
		} else if m != nil {
			payload = *m
		}
		if out := r.checkUnique(&payload, "", ctx, "packages.core.crud.post.create.error.existing"); out != nil {
			return r.itemFail(i, out, ctx)
		}
		row := new(E)
		copyAny(row, &payload)
		if !r.stamp(row, ctx) {
			return response.Forbidden(r.title(ctx), r.tr("packages.core.crud.error.no-scope", ctx))
		}
		payloads[i], rows[i] = payload, row
	}
	if err := r.db(ctx).Insert(rows...); err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}
	for i, row := range rows {
		_ = r.runHook(AfterCreate, row, ctx)
		_ = r.runHookCtx(AfterCreate, HookEvent{Entity: row, DTO: &payloads[i]}, ctx)
	}
	return response.Success(BulkCreated[E]{Items: rows}, r.title(ctx),
		r.trWith("packages.core.crud.bulk.create.success", ctx, map[string]any{"count": len(rows)}), nil)
}

// txEntity is a goose sql.Entity's transaction.
type txEntity[E any] interface {
	Tx(fn func(*gsql.Entity[E]) error) error
}

// BulkUpdate handles PATCH /bulk: it applies every item or none. Each item
// goes through Update's pipeline (partial ValidateBody, BeforeUpdate hooks and
// morphs, UniqueU, the stamp); every id must exist in the caller's scope
// before anything is written — an id from another workspace is "not found",
// never updated. Writes run in one transaction when the entity is a goose
// sql.Entity. Register it: router.Patch("/bulk", c.BulkUpdate).
func (r *CrudResource[E, C, U]) BulkUpdate(dto *BulkUpdateDto[U]) types.Output {
	ctx := dto.Ctx
	if out := r.guard(ctx); out != nil {
		return out
	}
	if len(dto.Items) == 0 {
		return response.BadRequest(r.title(ctx), r.tr("packages.core.crud.bulk.error.empty", ctx))
	}
	if len(dto.Items) > r.bulkLimit() {
		return response.BadRequest(r.title(ctx), r.trWith("packages.core.crud.bulk.error.too-many", ctx, map[string]any{"limit": r.bulkLimit()}))
	}
	type change struct {
		id      string
		payload U
		row     *E
		where   string
		args    []any
	}
	seen := map[string]bool{}
	changes := make([]change, len(dto.Items))
	for i, item := range dto.Items {
		id, payload := item.ID, item.Body
		if id == "" || seen[id] {
			return response.BadRequest(r.title(ctx), fmt.Sprintf("item %d: %s", i, r.tr("packages.core.crud.bulk.error.id", ctx)))
		}
		seen[id] = true
		if out := r.validateBody(&payload, true, ctx); out != nil {
			return r.itemFail(i, out, ctx)
		}
		if err := r.runHook(BeforeUpdate, &payload, ctx); err != nil {
			return r.itemFail(i, response.BadRequest(r.title(ctx), err.Error()), ctx)
		}
		if err := r.runHookCtx(BeforeUpdate, HookEvent{ID: id, DTO: &payload}, ctx); err != nil {
			return r.itemFail(i, response.BadRequest(r.title(ctx), err.Error()), ctx)
		}
		if m, err := r.runMorphU(BeforeUpdate, &payload, ctx); err != nil {
			return r.itemFail(i, response.BadRequest(r.title(ctx), err.Error()), ctx)
		} else if m != nil {
			payload = *m
		}
		where, args, ok := r.scopeWhere("id = ?", []any{id}, ctx)
		if !ok {
			return r.itemFail(i, response.BadRequest(r.title(ctx), r.tr("packages.core.crud.patch.update.error.not-found", ctx)), ctx)
		}
		existing, err := r.db(ctx).First(where, args...)
		if err != nil || existing == nil {
			return r.itemFail(i, response.BadRequest(r.title(ctx), r.tr("packages.core.crud.patch.update.error.not-found", ctx)), ctx)
		}
		if err := r.runHookCtx(BeforeUpdate, HookEvent{ID: id, DTO: &payload, Entity: existing}, ctx); err != nil {
			return r.itemFail(i, response.BadRequest(r.title(ctx), err.Error()), ctx)
		}
		if out := r.checkUniqueU(&payload, id, ctx, "packages.core.crud.patch.update.error.existing"); out != nil {
			return r.itemFail(i, out, ctx)
		}
		row := new(E)
		copyAny(row, &payload)
		if !r.stamp(row, ctx) {
			return response.Forbidden(r.title(ctx), r.tr("packages.core.crud.error.no-scope", ctx))
		}
		changes[i] = change{id: id, payload: payload, row: row, where: where, args: args}
	}

	apply := func(e types.Entity[E]) error {
		for _, c := range changes {
			if _, err := e.Update(c.row, c.where, c.args...); err != nil {
				return err
			}
		}
		return nil
	}
	var err error
	if tx, ok := r.db(ctx).(txEntity[E]); ok {
		err = tx.Tx(func(e *gsql.Entity[E]) error { return apply(e) })
	} else {
		err = apply(r.db(ctx))
	}
	if err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}
	ids := make([]string, len(changes))
	for i, c := range changes {
		ids[i] = c.id
		_ = r.runHook(AfterUpdate, c.row, ctx)
		_ = r.runHookCtx(AfterUpdate, HookEvent{ID: c.id, DTO: &changes[i].payload, Entity: c.row}, ctx)
	}
	return response.Success(BulkUpdated{IDs: ids}, r.title(ctx),
		r.trWith("packages.core.crud.bulk.update.success", ctx, map[string]any{"count": len(ids)}), nil)
}

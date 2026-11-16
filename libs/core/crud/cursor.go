package crud

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	gsql "github.com/awesome-goose/goose/modules/sql"
	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages/libs/core/filter"
	"github.com/thescaffold/gox-packages/libs/core/response"
)

// Cursor (keyset) pagination: GET /cursor?limit=50&cursor=<next_cursor>&order=desc
// plus the List filters. Rows are ordered by (created_at, id), so a page never
// skips or repeats a row when rows are added or removed meanwhile, and a deep
// page costs the same as the first. Needs an entity with "createdAt" and "id"
// JSON fields (goose sql.BaseEntity) built on goose sql.Entity.
//
// The cursor is opaque to clients but not secret: it only says where the next
// page starts. Every page is still scoped to the caller, so a forged cursor
// reveals nothing.

const (
	defaultCursorLimit = 25
	maxCursorLimit     = 100
)

// CursorMeta is the envelope's meta for a cursor page.
type CursorMeta struct {
	Limit      int     `json:"limit"`
	Order      string  `json:"order"`
	HasMore    bool    `json:"has_more"`
	NextCursor *string `json:"next_cursor"`
}

type cursorPos struct {
	T string `json:"t"`
	I string `json:"i"`
	O string `json:"o"`
}

// EncodeCursor makes the cursor that starts after a row (createdAt, id) in an
// order ("asc" or "desc"). ListCursor returns it as next_cursor.
func EncodeCursor(createdAt time.Time, id, order string) string {
	b, _ := json.Marshal(cursorPos{T: createdAt.UTC().Format(time.RFC3339Nano), I: id, O: order})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (time.Time, string, string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", "", false
	}
	var p cursorPos
	if json.Unmarshal(b, &p) != nil || p.I == "" || len(p.I) > 64 || (p.O != "asc" && p.O != "desc") {
		return time.Time{}, "", "", false
	}
	t, err := time.Parse(time.RFC3339Nano, p.T)
	if err != nil {
		return time.Time{}, "", "", false
	}
	return t, p.I, p.O, true
}

// orderedEntity is a goose sql.Entity's OrderBy.
type orderedEntity[E any] interface {
	OrderBy(sort string) (*gsql.Entity[E], error)
}

// ListCursor handles GET /cursor: one page of rows after the cursor, with
// meta {limit, order, has_more, next_cursor}. Query parameters: limit (1–100,
// default 25), order (asc, the default, or desc), cursor (the previous page's
// next_cursor; its order wins), and the same filters as List. Register it:
// router.Get("/cursor", c.ListCursor).
func (r *CrudResource[E, C, U]) ListCursor(dto *ListDto) types.Output {
	ctx := dto.Ctx
	if out := r.guard(ctx); out != nil {
		return out
	}
	queries := map[string]string{}
	for k, v := range dto.Queries {
		queries[k] = v
	}
	limit := defaultCursorLimit
	if v, err := strconv.Atoi(queries["limit"]); err == nil && v > 0 {
		limit = min(v, maxCursorLimit)
	}
	order := strings.ToLower(queries["order"])
	if order != "desc" {
		order = "asc"
	}
	var after *time.Time
	var afterID string
	if c := queries["cursor"]; c != "" {
		t, id, o, ok := decodeCursor(c)
		if !ok {
			return response.BadRequest(r.title(ctx), r.tr("packages.core.crud.get.cursor.error.invalid", ctx))
		}
		after, afterID, order = &t, id, o
	}
	for _, k := range []string{"limit", "order", "cursor", "page", "per_page"} {
		delete(queries, k)
	}

	_ = r.runHook(BeforeList, queries, ctx)
	f := filter.MakeFilter(queries, r.cfg.Searchable)
	where, args := conditionsToSQL(f.Conditions, f.DateFrom, f.DateTo)
	where, args, ok := r.scopeWhere(where, args, ctx)
	meta := CursorMeta{Limit: limit, Order: order}
	if !ok {
		return response.Success([]E{}, r.title(ctx), r.tr("packages.core.crud.get.all.success", ctx), meta)
	}
	if after != nil {
		cmp := ">"
		if order == "desc" {
			cmp = "<"
		}
		keyset := "(created_at " + cmp + " ? OR (created_at = ? AND id " + cmp + " ?))"
		if where != "" {
			where = "(" + where + ") AND " + keyset
		} else {
			where = keyset
		}
		args = append(args, *after, *after, afterID)
	}
	oe, isOrdered := r.db(ctx).(orderedEntity[E])
	if !isOrdered {
		return response.InternalServerError(r.cfg.Name, r.tr("packages.core.crud.get.cursor.error.unsupported", ctx))
	}
	dir := strings.ToUpper(order)
	sorted, err := oe.OrderBy("created_at " + dir + ", id " + dir)
	if err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	}
	if where == "" {
		where = "1 = 1" // goose reads a bare id from a query without args
	}
	rows, err := sorted.Find(0, limit+1, where, args...)
	if err != nil {
		return response.InternalServerError(r.cfg.Name, dbMsg(err))
	}
	if len(rows) > limit {
		rows = rows[:limit]
		meta.HasMore = true
	}
	if meta.HasMore && len(rows) > 0 {
		t, id, ok := rowPosition(&rows[len(rows)-1])
		if !ok {
			return response.InternalServerError(r.cfg.Name, r.tr("packages.core.crud.get.cursor.error.unsupported", ctx))
		}
		next := EncodeCursor(t, id, order)
		meta.NextCursor = &next
	}
	if m, err := r.runMorphAny(AfterList, rows, ctx); err != nil {
		return response.InternalServerError(r.cfg.Name, err.Error())
	} else if m != nil {
		if list, ok := m.([]E); ok {
			rows = list
		}
	}
	_ = r.runHook(AfterList, rows, ctx)
	if rows == nil {
		rows = []E{}
	}
	return response.Success(rows, r.title(ctx), r.tr("packages.core.crud.get.all.success", ctx), meta)
}

// rowPosition reads a row's createdAt and id through its JSON form.
func rowPosition(row any) (time.Time, string, bool) {
	b, err := json.Marshal(row)
	if err != nil {
		return time.Time{}, "", false
	}
	var p struct {
		ID        string     `json:"id"`
		CreatedAt *time.Time `json:"createdAt"`
	}
	if json.Unmarshal(b, &p) != nil || p.ID == "" || p.CreatedAt == nil {
		return time.Time{}, "", false
	}
	return *p.CreatedAt, p.ID, true
}

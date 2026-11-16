package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	gsql "github.com/awesome-goose/goose/modules/sql"
	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/crud"
	"github.com/thescaffold/gox-packages/libs/core/response"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// UP-4 against a real Postgres (CRUD_TEST_DB_URL, e.g.
// postgres://postgres@localhost:5432/envelope_cloud_w1c?sslmode=disable).

type pgNote struct {
	gsql.BaseEntity
	WorkspaceID string `gorm:"column:workspace_id" json:"workspaceId"`
	Title       string `gorm:"column:title" json:"title" binding:"required"`
}

func (pgNote) TableName() string { return "CrudW1cNotes" }

type noteBody struct {
	WorkspaceID string `json:"workspaceId"`
	Title       string `json:"title" binding:"required"`
}

func pgEntity(t *testing.T) *gsql.Entity[pgNote] {
	t.Helper()
	dsn := os.Getenv("CRUD_TEST_DB_URL")
	if dsn == "" {
		t.Skip("CRUD_TEST_DB_URL not set — skipping (needs a real Postgres)")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`DROP TABLE IF EXISTS "CrudW1cNotes"`)
	if err := db.Exec(`CREATE TABLE "CrudW1cNotes" (id varchar(36) PRIMARY KEY, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, deleted_at timestamptz, workspace_id varchar(36), title text)`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS "CrudW1cNotes"`) })
	e := gsql.NewEntity[pgNote](gsql.NewQuery(db))
	e.Hydrate("CrudW1cNotes", []string{"title"}, nil, nil, nil, nil, nil, "")
	return e
}

func res(e types.Entity[pgNote], cfg crud.Config[pgNote, noteBody, noteBody]) *crud.CrudResource[pgNote, noteBody, noteBody] {
	r := &crud.CrudResource[pgNote, noteBody, noteBody]{}
	r.Hydrate(e, cfg)
	return r
}

func in(ws string) ntxctx.NTXContext { return ntxctx.NTXContext{WorkspaceID: ws, UserID: "u-" + ws} }

func env(t *testing.T, out types.Output) response.Envelope {
	t.Helper()
	e, ok := out.Data().(response.Envelope)
	if !ok {
		t.Fatalf("output data is %T", out.Data())
	}
	return e
}

// asJSON round-trips v into dst.
func asJSON(t *testing.T, v, dst any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatal(err)
	}
}

// Both ways a resource can be tenant-scoped must hold: the WorkspaceScoped
// config, and an entity that requires a tenant (goose RequireTenant).
func scopedResources(t *testing.T) map[string]func() (*crud.CrudResource[pgNote, noteBody, noteBody], *gsql.Entity[pgNote]) {
	return map[string]func() (*crud.CrudResource[pgNote, noteBody, noteBody], *gsql.Entity[pgNote]){
		"WorkspaceScoped": func() (*crud.CrudResource[pgNote, noteBody, noteBody], *gsql.Entity[pgNote]) {
			e := pgEntity(t)
			return res(e, crud.Config[pgNote, noteBody, noteBody]{Name: "Note", WorkspaceScoped: true, ValidateBody: true, BulkLimit: 5}), e
		},
		"RequireTenant entity": func() (*crud.CrudResource[pgNote, noteBody, noteBody], *gsql.Entity[pgNote]) {
			e := pgEntity(t)
			e.RequireTenant("workspace_id")
			return res(e, crud.Config[pgNote, noteBody, noteBody]{Name: "Note", ValidateBody: true, BulkLimit: 5}), e
		},
	}
}

func TestCrudBulk_CrossTenantAndAllOrNothing(t *testing.T) {
	for name, mk := range scopedResources(t) {
		t.Run(name, func(t *testing.T) {
			r, e := mk()
			all := e.AcrossTenants()

			// create: a row naming workspace B is still created in A
			out := r.BulkCreate(&crud.BulkCreateDto[noteBody]{Items: []noteBody{{Title: "a1"}, {Title: "a2", WorkspaceID: "B"}, {Title: "a3"}}, Ctx: in("A")})
			if out.Code() != 200 {
				t.Fatalf("bulk create: %d %+v", out.Code(), env(t, out))
			}
			var created crud.BulkCreated[pgNote]
			asJSON(t, env(t, out).Data, &created)
			if len(created.Items) != 3 {
				t.Fatalf("created %d", len(created.Items))
			}
			if n, _ := all.Count("workspace_id = ?", "A"); n != 3 {
				t.Fatalf("%d rows in A, want 3 (none planted in B)", n)
			}
			r.BulkCreate(&crud.BulkCreateDto[noteBody]{Items: []noteBody{{Title: "b1"}}, Ctx: in("B")})
			var b1 pgNote
			if row, _ := all.First("title = ?", "b1"); row != nil {
				b1 = *row
			}

			// all-or-nothing: an invalid item writes nothing
			out = r.BulkCreate(&crud.BulkCreateDto[noteBody]{Items: []noteBody{{Title: "ok"}, {Title: ""}}, Ctx: in("A")})
			if out.Code() != 400 || !strings.HasPrefix(env(t, out).Message, "item 1:") {
				t.Fatalf("invalid item: %d %q", out.Code(), env(t, out).Message)
			}
			if n, _ := all.Count("title = ?", "ok"); n != 0 {
				t.Fatal("a failed batch wrote rows")
			}
			if out := r.BulkCreate(&crud.BulkCreateDto[noteBody]{Ctx: in("A")}); out.Code() != 400 {
				t.Fatal("empty batch accepted")
			}
			big := make([]noteBody, 6)
			for i := range big {
				big[i] = noteBody{Title: fmt.Sprint(i)}
			}
			if out := r.BulkCreate(&crud.BulkCreateDto[noteBody]{Items: big, Ctx: in("A")}); out.Code() != 400 {
				t.Fatal("batch over BulkLimit accepted")
			}
			if out := r.BulkCreate(&crud.BulkCreateDto[noteBody]{Items: []noteBody{{Title: "x"}}, Ctx: in("")}); out.Code() == 200 {
				t.Fatal("a caller with no workspace created rows")
			}

			// update: B cannot touch A's rows, and one foreign id sinks the batch
			a := created.Items
			out = r.BulkUpdate(&crud.BulkUpdateDto[noteBody]{Items: []crud.BulkUpdateItem[noteBody]{
				{ID: b1.Id, Body: noteBody{Title: "b1-new"}},
				{ID: a[0].Id, Body: noteBody{Title: "hacked"}},
			}, Ctx: in("B")})
			if out.Code() != 400 || !strings.HasPrefix(env(t, out).Message, "item 1:") {
				t.Fatalf("cross-tenant update: %d %q", out.Code(), env(t, out).Message)
			}
			if row, _ := all.First(a[0].Id); row.Title != "a1" {
				t.Fatal("B changed A's row")
			}
			if row, _ := all.First(b1.Id); row.Title != "b1" {
				t.Fatal("a rejected batch changed a row")
			}
			// a duplicate id is refused
			if out := r.BulkUpdate(&crud.BulkUpdateDto[noteBody]{Items: []crud.BulkUpdateItem[noteBody]{{ID: a[0].Id, Body: noteBody{Title: "x"}}, {ID: a[0].Id, Body: noteBody{Title: "y"}}}, Ctx: in("A")}); out.Code() != 400 {
				t.Fatal("duplicate id accepted")
			}
			// an update cannot move a row to another workspace
			out = r.BulkUpdate(&crud.BulkUpdateDto[noteBody]{Items: []crud.BulkUpdateItem[noteBody]{
				{ID: a[0].Id, Body: noteBody{Title: "a1-new"}},
				{ID: a[1].Id, Body: noteBody{Title: "a2-new", WorkspaceID: "B"}},
			}, Ctx: in("A")})
			// the stamp rewrites the workspace to A
			if out.Code() != 200 {
				t.Fatalf("own update: %d %+v", out.Code(), env(t, out))
			}
			if row, _ := all.First(a[1].Id); row.Title != "a2-new" || row.WorkspaceID != "A" {
				t.Fatalf("updated row %+v", row)
			}
			if n, _ := all.Count("workspace_id = ?", "A"); n != 3 {
				t.Fatalf("%d rows left in A, want 3", n)
			}
			// the JSON shape of an item is one object with its id
			var items []crud.BulkUpdateItem[noteBody]
			if err := json.Unmarshal([]byte(`[{"id":"x1","title":"t"}]`), &items); err != nil || items[0].ID != "x1" || items[0].Body.Title != "t" {
				t.Fatalf("item JSON: %+v %v", items, err)
			}
		})
	}
}

func TestCrudCursor_PagesWithoutGapsOrLeaks(t *testing.T) {
	for name, mk := range scopedResources(t) {
		t.Run(name, func(t *testing.T) {
			r, e := mk()
			base := time.Now().UTC().Add(-time.Hour)
			for i := 0; i < 7; i++ {
				ts := base.Add(time.Duration(i) * time.Second)
				if i == 4 {
					ts = base.Add(3 * time.Second) // a tie on created_at: the id breaks it
				}
				row := &pgNote{WorkspaceID: "A", Title: fmt.Sprintf("a%d", i)}
				row.CreatedAt = &ts
				if err := e.AcrossTenants().Insert(row); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 3; i++ {
				_ = e.AcrossTenants().Insert(&pgNote{WorkspaceID: "B", Title: fmt.Sprintf("b%d", i)})
			}

			page := func(q map[string]string, ws string) ([]pgNote, crud.CursorMeta, int) {
				out := r.ListCursor(&crud.ListDto{Queries: q, Ctx: in(ws)})
				en := env(t, out)
				var rows []pgNote
				var meta crud.CursorMeta
				asJSON(t, en.Data, &rows)
				asJSON(t, en.Meta, &meta)
				return rows, meta, out.Code()
			}
			for _, order := range []string{"asc", "desc"} {
				seen := map[string]bool{}
				var titles []string
				q := map[string]string{"limit": "3", "order": order}
				pages := 0
				for {
					rows, meta, code := page(q, "A")
					if code != 200 {
						t.Fatalf("%s page: %d", order, code)
					}
					pages++
					for _, row := range rows {
						if row.WorkspaceID != "A" {
							t.Fatalf("leaked row from %s", row.WorkspaceID)
						}
						if seen[row.Id] {
							t.Fatalf("row %s repeated", row.Title)
						}
						seen[row.Id] = true
						titles = append(titles, row.Title)
					}
					if !meta.HasMore {
						if meta.NextCursor != nil {
							t.Fatal("next_cursor on the last page")
						}
						break
					}
					q = map[string]string{"limit": "3", "cursor": *meta.NextCursor}
				}
				if len(seen) != 7 || pages != 3 {
					t.Fatalf("%s: %d rows over %d pages, want 7 over 3 (%v)", order, len(seen), pages, titles)
				}
				if order == "asc" && (titles[0] != "a0" || titles[6] != "a6") {
					t.Fatalf("asc order %v", titles)
				}
				if order == "desc" && (titles[0] != "a6" || titles[6] != "a0") {
					t.Fatalf("desc order %v", titles)
				}
			}

			// filters still apply
			rows, _, _ := page(map[string]string{"title": "a2"}, "A")
			if len(rows) != 1 || rows[0].Title != "a2" {
				t.Fatalf("filtered: %+v", rows)
			}
			// a cursor taken from A's pages and replayed by B shows only B's rows
			_, meta, _ := page(map[string]string{"limit": "1", "order": "desc"}, "A")
			rows, _, _ = page(map[string]string{"limit": "50", "cursor": *meta.NextCursor}, "B")
			for _, row := range rows {
				if row.WorkspaceID != "B" {
					t.Fatal("a replayed cursor leaked A's rows to B")
				}
			}
			// tampered cursors are refused, not parsed into SQL
			for _, bad := range []string{"%%%", "e30", crud.EncodeCursor(time.Now(), "x", "sideways")} {
				if _, _, code := page(map[string]string{"cursor": bad}, "A"); code != 400 {
					t.Fatalf("cursor %q: %d", bad, code)
				}
			}
			// no workspace: nothing, not everything
			rows, _, code := page(map[string]string{}, "")
			if code == 200 && len(rows) != 0 {
				t.Fatalf("no workspace listed %d rows", len(rows))
			}
		})
	}
}

func TestCrudTenantGuard(t *testing.T) {
	e := pgEntity(t)
	unscoped := res(e, crud.Config[pgNote, noteBody, noteBody]{Name: "LooseNote"})
	global := res(e, crud.Config[pgNote, noteBody, noteBody]{Name: "SharedNote", Global: true})
	scoped := res(e, crud.Config[pgNote, noteBody, noteBody]{Name: "TenantNote", WorkspaceScoped: true})

	found := false
	for _, n := range crud.UnscopedResources() {
		if strings.HasPrefix(n, "SharedNote") || strings.HasPrefix(n, "TenantNote") {
			t.Fatalf("%s listed as unscoped", n)
		}
		if strings.HasPrefix(n, "LooseNote") {
			found = true
		}
	}
	if !found {
		t.Fatalf("LooseNote missing from UnscopedResources: %v", crud.UnscopedResources())
	}

	// off (the default): unchanged behaviour
	if out := unscoped.List(&crud.ListDto{Ctx: in("A")}); out.Code() != 200 {
		t.Fatalf("guard off: %d", out.Code())
	}
	crud.RequireTenantScope(true)
	defer crud.RequireTenantScope(false)
	if out := unscoped.List(&crud.ListDto{Ctx: in("A")}); out.Code() != 500 {
		t.Fatalf("guard on, unscoped List: %d", out.Code())
	}
	if out := unscoped.Create(&crud.CreateDto[noteBody]{Body: noteBody{Title: "x"}, Ctx: in("A")}); out.Code() != 500 {
		t.Fatalf("guard on, unscoped Create: %d", out.Code())
	}
	if out := unscoped.BulkCreate(&crud.BulkCreateDto[noteBody]{Items: []noteBody{{Title: "x"}}, Ctx: in("A")}); out.Code() != 500 {
		t.Fatalf("guard on, unscoped BulkCreate: %d", out.Code())
	}
	if n, _ := e.Count(""); n != 0 {
		t.Fatal("a guarded request wrote a row")
	}
	if out := global.List(&crud.ListDto{Ctx: in("A")}); out.Code() != 200 {
		t.Fatalf("guard on, Global: %d", out.Code())
	}
	if out := scoped.Create(&crud.CreateDto[noteBody]{Body: noteBody{Title: "x"}, Ctx: in("A")}); out.Code() != 200 {
		t.Fatalf("guard on, scoped: %d", out.Code())
	}
}

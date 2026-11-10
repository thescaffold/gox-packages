package tests

import (
	"net/http"
	"testing"

	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/crud"
)

// A scoped resource's writes land in the caller's own workspace (and, OwnerScoped, as the
// caller), whatever the body names; a caller with no scope cannot write at all (PLAN U26).

type stampBase struct {
	ID string `gorm:"column:id" json:"id"`
}

type stampedRow struct {
	stampBase
	UserId      *string `gorm:"column:user_id;type:varchar(36)" json:"userId,omitempty"`
	WorkspaceId string  `gorm:"column:workspace_id;type:varchar(36)" json:"workspaceId"`
	Name        string  `gorm:"column:name" json:"name"`
}

func newStampResource(cfg crud.Config[stampedRow, stampedRow, stampedRow]) (*crud.CrudResource[stampedRow, stampedRow, stampedRow], *mockEntity[stampedRow]) {
	entity := &mockEntity[stampedRow]{}
	r := &crud.CrudResource[stampedRow, stampedRow, stampedRow]{}
	r.Hydrate(entity, cfg)
	return r, entity
}

func TestScopedCreateTakesTheCallersWorkspaceAndUser(t *testing.T) {
	r, entity := newStampResource(crud.Config[stampedRow, stampedRow, stampedRow]{Name: "row", WorkspaceScoped: true, OwnerScoped: true})
	other := "user-b"
	out := r.Create(&crud.CreateDto[stampedRow]{
		Body: stampedRow{Name: "x", WorkspaceId: "ws-b", UserId: &other},
		Ctx:  ntxctx.NTXContext{UserID: "user-a", WorkspaceID: "ws-a"},
	})
	if out.Code() != http.StatusOK || len(entity.items) != 1 {
		t.Fatalf("create = %d, %d rows", out.Code(), len(entity.items))
	}
	got := entity.items[0]
	if got.WorkspaceId != "ws-a" || got.UserId == nil || *got.UserId != "user-a" {
		t.Fatalf("row landed in workspace %q as %v; want ws-a as user-a", got.WorkspaceId, got.UserId)
	}
}

func TestScopedCreateWithoutAScopeIsRefused(t *testing.T) {
	r, entity := newStampResource(crud.Config[stampedRow, stampedRow, stampedRow]{Name: "row", WorkspaceScoped: true})
	out := r.Create(&crud.CreateDto[stampedRow]{Body: stampedRow{Name: "x", WorkspaceId: "ws-b"}})
	if out.Code() != http.StatusForbidden || entity.insertCalled {
		t.Fatalf("create with no workspace = %d (inserted %v); want 403, nothing written", out.Code(), entity.insertCalled)
	}
}

func TestScopedUpdateCannotMoveARowToAnotherWorkspace(t *testing.T) {
	r, entity := newStampResource(crud.Config[stampedRow, stampedRow, stampedRow]{Name: "row", WorkspaceScoped: true})
	entity.items = []stampedRow{{stampBase: stampBase{ID: "r1"}, WorkspaceId: "ws-a"}}
	out := r.Update(&crud.UpdateDto[stampedRow]{ID: "r1", Body: stampedRow{Name: "y", WorkspaceId: "ws-b"}, Ctx: ntxctx.NTXContext{WorkspaceID: "ws-a"}})
	if out.Code() != http.StatusOK || entity.lastChanges == nil {
		t.Fatalf("update = %d", out.Code())
	}
	if entity.lastChanges.WorkspaceId != "ws-a" {
		t.Fatalf("update moved the row to %q", entity.lastChanges.WorkspaceId)
	}
}

func TestUnscopedCreateKeepsTheBody(t *testing.T) {
	r, entity := newStampResource(crud.Config[stampedRow, stampedRow, stampedRow]{Name: "row"})
	out := r.Create(&crud.CreateDto[stampedRow]{Body: stampedRow{Name: "x", WorkspaceId: "ws-b"}})
	if out.Code() != http.StatusOK || entity.items[0].WorkspaceId != "ws-b" {
		t.Fatalf("an unscoped resource changed behaviour: %d %+v", out.Code(), entity.items)
	}
}

func TestCrudResourceDocumentsItsHandlers(t *testing.T) {
	r, _ := newStampResource(crud.Config[stampedRow, stampedRow, stampedRow]{Name: "row"})
	if _, ok := r.OpenAPIFor("List").Response.([]stampedRow); !ok {
		t.Error("List should document []E")
	}
	if _, ok := r.OpenAPIFor("Get").Response.(stampedRow); !ok {
		t.Error("Get should document E")
	}
	if _, ok := r.OpenAPIFor("Update").Response.(crud.Updated); !ok {
		t.Error("Update should document its id")
	}
	if r.OpenAPIFor("Delete").Response != nil {
		t.Error("Delete answers no data")
	}
}

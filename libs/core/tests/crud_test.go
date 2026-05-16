package tests

import (
	"errors"
	"net/http"
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/crud"
	"github.com/thescaffold/gox-packages/libs/core/response"
	"github.com/thescaffold/gox-packages/libs/core/utils"
)

func TestCrud(t *testing.T) {
	test.NewSuiteRunner(t, &CrudSuite{}).Run()
}

type CrudSuite struct {
	test.Suite
}

// ── fixture types ──────────────────────────────────────────────────────────────

type Item struct {
	ID   string
	Name string
}

// mockEntity[T] implements types.Entity[T] with in-memory storage.
type mockEntity[T any] struct {
	items        []T
	existsResult bool
	insertErr    error
	insertCalled bool
	deleteCalled bool
	updateCalled bool
}

func (m *mockEntity[T]) Hydrate(name string, searchable, relations []string,
	scope func() (string, []any), unique func(*T) (any, []any),
	morphs map[string]func(*T), hooks map[string]func(*T) error, sort string) {
}
func (m *mockEntity[T]) Name() string { return "mock" }
func (m *mockEntity[T]) Exists(query string, args ...any) (bool, error) {
	return m.existsResult, nil
}
func (m *mockEntity[T]) Count(query string, args ...any) (int64, error) {
	return int64(len(m.items)), nil
}
func (m *mockEntity[T]) Sum(col, query string, args ...any) (int64, error)   { return 0, nil }
func (m *mockEntity[T]) Avg(col, query string, args ...any) (float64, error) { return 0, nil }
func (m *mockEntity[T]) Min(col, query string, args ...any) (int64, error)   { return 0, nil }
func (m *mockEntity[T]) Max(col, query string, args ...any) (int64, error)   { return 0, nil }
func (m *mockEntity[T]) Insert(entities ...*T) error {
	m.insertCalled = true
	if m.insertErr != nil {
		return m.insertErr
	}
	for _, e := range entities {
		m.items = append(m.items, *e)
	}
	return nil
}
func (m *mockEntity[T]) Upsert(entities ...*T) error {
	m.insertCalled = true
	for _, e := range entities {
		m.items = append(m.items, *e)
	}
	return nil
}
func (m *mockEntity[T]) Update(changes *T, query string, args ...any) (int64, error) {
	m.updateCalled = true
	return 1, nil
}
func (m *mockEntity[T]) First(query string, args ...any) (*T, error) {
	if len(m.items) == 0 {
		return nil, nil
	}
	return &m.items[0], nil
}
func (m *mockEntity[T]) Last(query string, args ...any) (*T, error) {
	if len(m.items) == 0 {
		return nil, nil
	}
	last := m.items[len(m.items)-1]
	return &last, nil
}
func (m *mockEntity[T]) Some(query string, args ...any) ([]T, error) { return m.items, nil }
func (m *mockEntity[T]) Page(page, perPage int, query string, args ...any) ([]T, error) {
	return m.items, nil
}
func (m *mockEntity[T]) All() ([]T, error) { return m.items, nil }
func (m *mockEntity[T]) Find(offset, limit int, query string, args ...any) ([]T, error) {
	return m.items, nil
}
func (m *mockEntity[T]) Delete(query string, args ...any) (int64, error) {
	m.deleteCalled = true
	return 1, nil
}
func (m *mockEntity[T]) BuildQuery(queries map[string]string) (string, []any) { return "", nil }

// helper: build a hydrated CrudResource and its mock entity.
func newItemResource(cfg crud.Config[Item, Item, Item]) (*crud.CrudResource[Item, Item, Item], *mockEntity[Item]) {
	entity := &mockEntity[Item]{}
	r := &crud.CrudResource[Item, Item, Item]{}
	r.Hydrate(entity, cfg)
	return r, entity
}

// helper: extract response envelope from types.Output.
func envelope(out types.Output) response.Envelope {
	return out.Data().(response.Envelope)
}

// ── QueriesMiddleware ──────────────────────────────────────────────────────────

func (s *CrudSuite) TestQueriesMiddleware_SetsContext() {
	mw := &crud.QueriesMiddleware{}
	ctx := test.NewMockContext()
	ctx.MockRequest().WithQueries(map[string]string{"page": "2"})
	err := mw.Handle(ctx)
	s.T.Expect(err).ToBeNil()
	val := ctx.GetValue("queries")
	s.T.Expect(val == nil).ToEqual(false)
}

// ── Create ─────────────────────────────────────────────────────────────────────

func (s *CrudSuite) TestCreate_Returns200() {
	// TS create() returns success() → HTTP 200, not 201.
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	out := r.Create(&crud.CreateDto[Item]{Body: Item{Name: "Alpha"}})
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
	s.T.Expect(envelope(out).Status).ToEqual("success")
	s.T.Expect(entity.insertCalled).ToEqual(true)
}

func (s *CrudSuite) TestCreate_UniqueConflict_Returns400() {
	// TS create() raises error() with no status arg → defaults to BAD_REQUEST.
	entity := &mockEntity[Item]{existsResult: true}
	r := &crud.CrudResource[Item, Item, Item]{}
	r.Hydrate(entity, crud.Config[Item, Item, Item]{
		Name: "Item",
		Unique: func(c *Item) []utils.KeyValue {
			return []utils.KeyValue{{"name": c.Name}}
		},
	})
	out := r.Create(&crud.CreateDto[Item]{Body: Item{Name: "Alpha"}})
	s.T.Expect(out.Code()).ToEqual(http.StatusBadRequest)
	s.T.Expect(envelope(out).Status).ToEqual("error")
}

func (s *CrudSuite) TestCreate_InsertError_Returns500() {
	entity := &mockEntity[Item]{insertErr: errors.New("db error")}
	r := &crud.CrudResource[Item, Item, Item]{}
	r.Hydrate(entity, crud.Config[Item, Item, Item]{Name: "Item"})
	out := r.Create(&crud.CreateDto[Item]{Body: Item{Name: "Alpha"}})
	s.T.Expect(out.Code()).ToEqual(http.StatusInternalServerError)
}

func (s *CrudSuite) TestCreate_BeforeHookCalled() {
	hookCalled := false
	r, _ := newItemResource(crud.Config[Item, Item, Item]{
		Name:  "Item",
		Hooks: map[string]crud.HookFn{crud.BeforeCreate: func(payload any, ctx ntxctx.NTXContext) error { hookCalled = true; return nil }},
	})
	r.Create(&crud.CreateDto[Item]{Body: Item{Name: "Alpha"}})
	s.T.Expect(hookCalled).ToEqual(true)
}

func (s *CrudSuite) TestCreate_AfterHookCalled() {
	hookCalled := false
	r, _ := newItemResource(crud.Config[Item, Item, Item]{
		Name:  "Item",
		Hooks: map[string]crud.HookFn{crud.AfterCreate: func(payload any, ctx ntxctx.NTXContext) error { hookCalled = true; return nil }},
	})
	r.Create(&crud.CreateDto[Item]{Body: Item{Name: "Alpha"}})
	s.T.Expect(hookCalled).ToEqual(true)
}

// ── List ───────────────────────────────────────────────────────────────────────

func (s *CrudSuite) TestList_Returns200() {
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	entity.items = []Item{{ID: "1", Name: "A"}, {ID: "2", Name: "B"}}
	out := r.List(&crud.ListDto{Queries: map[string]string{}})
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
	s.T.Expect(envelope(out).Status).ToEqual("success")
}

func (s *CrudSuite) TestList_PaginationMeta_Present() {
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	entity.items = []Item{{ID: "1", Name: "A"}}
	out := r.List(&crud.ListDto{Queries: map[string]string{"page": "1", "perPage": "10"}})
	s.T.Expect(envelope(out).Meta == nil).ToEqual(false)
}

// ── Get ────────────────────────────────────────────────────────────────────────

func (s *CrudSuite) TestGet_Found_Returns200() {
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	entity.items = []Item{{ID: "abc", Name: "A"}}
	out := r.Get(&crud.GetDto{ID: "abc"})
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
	s.T.Expect(envelope(out).Status).ToEqual("success")
}

func (s *CrudSuite) TestGet_NotFound_Returns404() {
	r, _ := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	// no items seeded
	out := r.Get(&crud.GetDto{ID: "missing"})
	s.T.Expect(out.Code()).ToEqual(http.StatusNotFound)
	s.T.Expect(envelope(out).Status).ToEqual("error")
}

// ── Update ─────────────────────────────────────────────────────────────────────

func (s *CrudSuite) TestUpdate_Found_Returns200() {
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	entity.items = []Item{{ID: "abc", Name: "A"}}
	out := r.Update(&crud.UpdateDto[Item]{ID: "abc", Body: Item{Name: "B"}})
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
	s.T.Expect(envelope(out).Status).ToEqual("success")
	s.T.Expect(entity.updateCalled).ToEqual(true)
}

func (s *CrudSuite) TestUpdate_NotFound_Returns400() {
	// TS updatePatch() raises error() with no status arg → BAD_REQUEST, not 404.
	r, _ := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	out := r.Update(&crud.UpdateDto[Item]{ID: "missing", Body: Item{Name: "B"}})
	s.T.Expect(out.Code()).ToEqual(http.StatusBadRequest)
}

// ── Delete ─────────────────────────────────────────────────────────────────────

func (s *CrudSuite) TestDelete_Found_Returns200() {
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	entity.items = []Item{{ID: "abc", Name: "A"}}
	out := r.Delete(&crud.DeleteDto{ID: "abc"})
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
	s.T.Expect(entity.deleteCalled).ToEqual(true)
}

func (s *CrudSuite) TestDelete_NotFound_Returns404() {
	r, _ := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	out := r.Delete(&crud.DeleteDto{ID: "ghost"})
	s.T.Expect(out.Code()).ToEqual(http.StatusNotFound)
}

// ── Metrics ────────────────────────────────────────────────────────────────────

func (s *CrudSuite) TestMetrics_ReturnsCount() {
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	entity.items = []Item{{}, {}, {}}
	out := r.Metrics(&crud.ListDto{Queries: map[string]string{}})
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
	env := envelope(out)
	data, ok := env.Data.(map[string]any)
	s.T.Expect(ok).ToEqual(true)
	// TS metrics() returns data { entities: <count> }.
	s.T.Expect(data["entities"]).ToEqual(int64(3))
}

// ── FindByIds ──────────────────────────────────────────────────────────────────

func (s *CrudSuite) TestFindByIds_EmptySlice_Returns400() {
	r, _ := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	out := r.FindByIds(&crud.FindByIdsDto{IDs: nil})
	s.T.Expect(out.Code()).ToEqual(http.StatusBadRequest)
}

func (s *CrudSuite) TestFindByIds_WithIds_Returns200() {
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	entity.items = []Item{{ID: "1", Name: "A"}, {ID: "2", Name: "B"}}
	out := r.FindByIds(&crud.FindByIdsDto{
		IDs:     []string{"1", "2"},
		Queries: map[string]string{},
	})
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
}

// ── FindByType ─────────────────────────────────────────────────────────────────

func (s *CrudSuite) TestFindByType_Latest_Returns200() {
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	entity.items = []Item{{ID: "old"}, {ID: "new"}}
	out := r.FindByType(&crud.FindByTypeDto{Type: "latest", Queries: map[string]string{}})
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
}

func (s *CrudSuite) TestFindByType_Oldest_Returns200() {
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	entity.items = []Item{{ID: "old"}}
	out := r.FindByType(&crud.FindByTypeDto{Type: "oldest", Queries: map[string]string{}})
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
}

func (s *CrudSuite) TestFindByType_NotFound_Returns404() {
	r, _ := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	out := r.FindByType(&crud.FindByTypeDto{Type: "latest", Queries: map[string]string{}})
	s.T.Expect(out.Code()).ToEqual(http.StatusNotFound)
}

// ── FindRelatives ──────────────────────────────────────────────────────────────

func (s *CrudSuite) TestFindRelatives_Found_Returns200() {
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	entity.items = []Item{{ID: "abc"}}
	out := r.FindRelatives(&crud.GetDto{ID: "abc", Relative: "tags"})
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
}

func (s *CrudSuite) TestFindRelatives_NotFound_Returns404() {
	r, _ := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	out := r.FindRelatives(&crud.GetDto{ID: "missing", Relative: "tags"})
	s.T.Expect(out.Code()).ToEqual(http.StatusNotFound)
}

// ── CreateIgnoreDuplicate ──────────────────────────────────────────────────────

func (s *CrudSuite) TestCreateIgnoreDuplicate_Returns200() {
	// TS createIgnoreDuplicate() returns success() → HTTP 200, not 201.
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	out := r.CreateIgnoreDuplicate(&crud.CreateDto[Item]{Body: Item{Name: "Beta"}})
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
	s.T.Expect(entity.insertCalled).ToEqual(true)
}

// ── Upsert ─────────────────────────────────────────────────────────────────────

func (s *CrudSuite) TestUpsert_NoUpdateParam_Inserts() {
	r, entity := newItemResource(crud.Config[Item, Item, Item]{Name: "Item"})
	out := r.Upsert(&crud.UpsertDto[Item]{Body: Item{Name: "Gamma"}})
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
	s.T.Expect(entity.insertCalled).ToEqual(true)
}

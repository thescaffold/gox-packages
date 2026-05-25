package tests

import (
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages/libs/core/filter"
)

func TestFilter(t *testing.T) {
	test.NewSuiteRunner(t, &FilterSuite{}).Run()
}

type FilterSuite struct {
	test.Suite
}

// --- pagination defaults ---

func (s *FilterSuite) TestDefaults_Page1_PerPage12() {
	r := filter.MakeFilter(map[string]string{}, nil)
	s.T.Expect(r.Page).ToEqual(1)
	s.T.Expect(r.PerPage).ToEqual(12)
}

func (s *FilterSuite) TestPage_CustomValues() {
	r := filter.MakeFilter(map[string]string{"page": "3", "perPage": "25"}, nil)
	s.T.Expect(r.Page).ToEqual(3)
	s.T.Expect(r.PerPage).ToEqual(25)
}

func (s *FilterSuite) TestOffset_Page1() {
	r := filter.MakeFilter(map[string]string{}, nil)
	s.T.Expect(r.Offset()).ToEqual(0)
}

func (s *FilterSuite) TestOffset_Page3_PerPage10() {
	r := filter.MakeFilter(map[string]string{"page": "3", "perPage": "10"}, nil)
	s.T.Expect(r.Offset()).ToEqual(20)
}

// --- columns & relations ---

func (s *FilterSuite) TestColumns_Empty() {
	r := filter.MakeFilter(map[string]string{}, nil)
	s.T.Expect(len(r.Columns)).ToEqual(0)
}

func (s *FilterSuite) TestColumns_IncludesIdAndCreatedAt() {
	r := filter.MakeFilter(map[string]string{"columns": "name,email"}, nil)
	s.T.Expect(r.Columns[0]).ToEqual("id")
	s.T.Expect(r.Columns[1]).ToEqual("created_at")
	s.T.Expect(len(r.Columns)).ToEqual(4) // id, created_at, name, email
}

func (s *FilterSuite) TestRelations_Split() {
	r := filter.MakeFilter(map[string]string{"relations": "user,workspace"}, nil)
	s.T.Expect(len(r.Relations)).ToEqual(2)
	s.T.Expect(r.Relations[0]).ToEqual("user")
	s.T.Expect(r.Relations[1]).ToEqual("workspace")
}

// --- LIKE search ---

func (s *FilterSuite) TestSearch_NoQuery_NoConditions() {
	r := filter.MakeFilter(map[string]string{}, []string{"name", "code"})
	s.T.Expect(len(r.Conditions)).ToEqual(0)
}

func (s *FilterSuite) TestSearch_QueryBuildsLike() {
	r := filter.MakeFilter(map[string]string{"query": "hello"}, []string{"name", "code"})
	// one condition per searchable field
	s.T.Expect(len(r.Conditions)).ToEqual(2)
	nameVal := r.Conditions[0]["name"]
	like, ok := nameVal.(filter.Like)
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(like.Pattern()).ToEqual("%hello%")
}

// --- exact field filters ---

func (s *FilterSuite) TestFieldFilter_SimpleKey() {
	r := filter.MakeFilter(map[string]string{"status": "active"}, nil)
	s.T.Expect(len(r.Conditions)).ToEqual(1)
	s.T.Expect(r.Conditions[0]["status"]).ToEqual("active")
}

func (s *FilterSuite) TestFieldFilter_ReservedKeyIgnored() {
	r := filter.MakeFilter(map[string]string{
		"scope": "global",
		"page":  "2",
	}, nil)
	s.T.Expect(len(r.Conditions)).ToEqual(0)
	s.T.Expect(r.Page).ToEqual(2)
}

func (s *FilterSuite) TestFieldFilter_MultipleKeys() {
	r := filter.MakeFilter(map[string]string{"status": "active", "type": "premium"}, nil)
	s.T.Expect(len(r.Conditions)).ToEqual(1)
	s.T.Expect(r.Conditions[0]["status"]).ToEqual("active")
	s.T.Expect(r.Conditions[0]["type"]).ToEqual("premium")
}

// --- dotted key expansion ---

func (s *FilterSuite) TestDottedKey_ExpandsToNested() {
	r := filter.MakeFilter(map[string]string{"user.name": "alice"}, nil)
	s.T.Expect(len(r.Conditions)).ToEqual(1)
	user, ok := r.Conditions[0]["user"].(map[string]any)
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(user["name"]).ToEqual("alice")
}

func (s *FilterSuite) TestDottedKey_ThreeLevels() {
	r := filter.MakeFilter(map[string]string{"a.b.c": "val"}, nil)
	l1 := r.Conditions[0]["a"].(map[string]any)
	l2 := l1["b"].(map[string]any)
	s.T.Expect(l2["c"]).ToEqual("val")
}

// --- LIKE + field filter combined ---

func (s *FilterSuite) TestSearch_CombinedWithFieldFilter() {
	r := filter.MakeFilter(
		map[string]string{"query": "foo", "status": "active"},
		[]string{"name"},
	)
	// Each LIKE condition should also carry the field filter
	s.T.Expect(len(r.Conditions)).ToEqual(1)
	_, hasLike := r.Conditions[0]["name"].(filter.Like)
	s.T.Expect(hasLike).ToEqual(true)
	s.T.Expect(r.Conditions[0]["status"]).ToEqual("active")
}

// --- date range ---

func (s *FilterSuite) TestDateRange_Captured() {
	r := filter.MakeFilter(map[string]string{"from": "2024-01-01", "to": "2024-12-31"}, nil)
	s.T.Expect(r.DateFrom).ToEqual("2024-01-01")
	s.T.Expect(r.DateTo).ToEqual("2024-12-31")
}

func (s *FilterSuite) TestDateRange_Empty_WhenNotProvided() {
	r := filter.MakeFilter(map[string]string{}, nil)
	s.T.Expect(r.DateFrom).ToEqual("")
	s.T.Expect(r.DateTo).ToEqual("")
}

// --- SQL error mapping (GetSQLError / DefaultSQLMapper) ---
// Mirrors TS getSqlError() + error.filter.ts QueryFailedError handling.

func (s *FilterSuite) TestGetSQLError_PostgresUnique() {
	s.T.Expect(filter.GetSQLError("23505")).ToEqual("This value already exists. Please choose a different value.")
}

func (s *FilterSuite) TestGetSQLError_MysqlDuplicate() {
	s.T.Expect(filter.GetSQLError("1062")).ToEqual("The provided value already exists. Please choose a different value.")
}

func (s *FilterSuite) TestGetSQLError_MysqlNumericCode() {
	// TS getSqlError stringifies the code, so a numeric errno resolves too.
	s.T.Expect(filter.GetSQLError(1213)).ToEqual("A deadlock was detected. Please retry the transaction.")
}

func (s *FilterSuite) TestGetSQLError_UnknownFallback() {
	s.T.Expect(filter.GetSQLError("99999")).ToEqual("Check your request and try again, something is not quite right with it")
}

func (s *FilterSuite) TestDefaultSQLMapper_Postgres() {
	err := errorString(`ERROR: duplicate key value violates unique constraint "users_email_key" (SQLSTATE 23505)`)
	handled, msg := filter.DefaultSQLMapper(err)
	s.T.Expect(handled).ToEqual(true)
	s.T.Expect(msg).ToEqual("This value already exists. Please choose a different value.")
}

func (s *FilterSuite) TestDefaultSQLMapper_Mysql() {
	err := errorString("Error 1062 (23000): Duplicate entry 'a@b.com' for key 'users.email'")
	handled, msg := filter.DefaultSQLMapper(err)
	s.T.Expect(handled).ToEqual(true)
	s.T.Expect(msg).ToEqual("The provided value already exists. Please choose a different value.")
}

func (s *FilterSuite) TestDefaultSQLMapper_NonSQLError() {
	handled, _ := filter.DefaultSQLMapper(errorString("some random failure"))
	s.T.Expect(handled).ToEqual(false)
}

type errorString string

func (e errorString) Error() string { return string(e) }

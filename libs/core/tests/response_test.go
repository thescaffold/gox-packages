package tests

import (
	"net/http"
	"testing"

	"github.com/thescaffold/gox-packages-core/response"
	test "github.com/awesome-goose/goose/testing"
)

func TestResponse(t *testing.T) {
	test.NewSuiteRunner(t, &ResponseSuite{}).Run()
}

type ResponseSuite struct {
	test.Suite
}

func (s *ResponseSuite) TestSuccess_StatusCode() {
	out := response.Success(map[string]any{"id": "1"}, "Test", "ok", nil)
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
}

func (s *ResponseSuite) TestSuccess_EnvelopeShape() {
	out := response.Success("hello", "Title", "message", nil)
	env, ok := out.Data().(response.Envelope)
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(env.Status).ToEqual("success")
	s.T.Expect(env.Title).ToEqual("Title")
	s.T.Expect(env.Message).ToEqual("message")
	s.T.Expect(env.Data).ToEqual("hello")
}

func (s *ResponseSuite) TestSuccess_WithMeta() {
	meta := map[string]any{"count": 3}
	out := response.Success(nil, "", "", meta)
	env := out.Data().(response.Envelope)
	s.T.Expect(env.Meta).ToEqual(meta)
}

func (s *ResponseSuite) TestCreated_StatusCode() {
	out := response.Created(nil, "", "")
	s.T.Expect(out.Code()).ToEqual(http.StatusCreated)
}

func (s *ResponseSuite) TestCreated_EnvelopeShape() {
	out := response.Created("data", "T", "M")
	env := out.Data().(response.Envelope)
	s.T.Expect(env.Status).ToEqual("success")
	s.T.Expect(env.Data).ToEqual("data")
}

func (s *ResponseSuite) TestError_StatusCode() {
	out := response.Error("T", "M", http.StatusBadRequest)
	s.T.Expect(out.Code()).ToEqual(http.StatusBadRequest)
}

func (s *ResponseSuite) TestError_EnvelopeShape() {
	out := response.Error("Title", "something broke", http.StatusBadRequest)
	env := out.Data().(response.Envelope)
	s.T.Expect(env.Status).ToEqual("error")
	s.T.Expect(env.Title).ToEqual("Title")
	s.T.Expect(env.Message).ToEqual("something broke")
	s.T.Expect(env.Data).ToBeNil()
}

func (s *ResponseSuite) TestNotFound_Code() {
	s.T.Expect(response.NotFound("T", "M").Code()).ToEqual(http.StatusNotFound)
}

func (s *ResponseSuite) TestConflict_Code() {
	s.T.Expect(response.Conflict("T", "M").Code()).ToEqual(http.StatusConflict)
}

func (s *ResponseSuite) TestUnauthorized_Code() {
	s.T.Expect(response.Unauthorized("T", "M").Code()).ToEqual(http.StatusUnauthorized)
}

func (s *ResponseSuite) TestForbidden_Code() {
	s.T.Expect(response.Forbidden("T", "M").Code()).ToEqual(http.StatusForbidden)
}

func (s *ResponseSuite) TestInternalServerError_Code() {
	s.T.Expect(response.InternalServerError("T", "M").Code()).ToEqual(http.StatusInternalServerError)
}

func (s *ResponseSuite) TestContentType_IsJSON() {
	out := response.Success(nil, "", "", nil)
	s.T.Expect(out.ContentType()).ToEqual("application/json; charset=utf-8")
}

func (s *ResponseSuite) TestPaginated_StatusCode() {
	out := response.Paginated([]any{}, 1, 10, 100, "T", "M")
	s.T.Expect(out.Code()).ToEqual(http.StatusOK)
}

func (s *ResponseSuite) TestPaginated_MetaShape() {
	out := response.Paginated([]string{"a", "b"}, 2, 10, 25, "T", "M")
	env := out.Data().(response.Envelope)
	meta, ok := env.Meta.(response.PaginationMeta)
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(meta.CurrentPage).ToEqual(2)
	s.T.Expect(meta.PerPage).ToEqual(10)
	s.T.Expect(meta.Total).ToEqual(int64(25))
}

func (s *ResponseSuite) TestPaginated_NextPage() {
	out := response.Paginated(nil, 1, 10, 25, "", "")
	meta := out.Data().(response.Envelope).Meta.(response.PaginationMeta)
	s.T.Expect(meta.NextPage).Not().ToBeNil()
	s.T.Expect(*meta.NextPage).ToEqual(2)
	s.T.Expect(meta.PrevPage).ToBeNil()
}

func (s *ResponseSuite) TestPaginated_LastPage_NoNext() {
	out := response.Paginated(nil, 3, 10, 25, "", "") // 3 pages total, on page 3
	meta := out.Data().(response.Envelope).Meta.(response.PaginationMeta)
	s.T.Expect(meta.NextPage).ToBeNil()
	s.T.Expect(meta.PrevPage).Not().ToBeNil()
	s.T.Expect(*meta.PrevPage).ToEqual(2)
}

func (s *ResponseSuite) TestPaginated_DataPreserved() {
	items := []string{"x", "y"}
	env := response.Paginated(items, 1, 10, 2, "", "").Data().(response.Envelope)
	s.T.Expect(env.Data).ToEqual(items)
}

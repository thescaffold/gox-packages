package response

import (
	"net/http"
	"strings"

	"github.com/awesome-goose/goose/types"
)

// Envelope is the standard NTX API response body.
// Matches the TypeScript BaseResponseBody (jsx-*/common/utils/values.ts) shape exactly:
// {"status":"success|error","title":"...","message":"...","data":{...},"meta":{...},"raw":"...","headers":{...}}
// An error adds a stable machine-readable code, whether retrying can help, and
// per-field problems when the input was invalid. Success bodies never carry them.
type Envelope struct {
	Status    string            `json:"status"`
	Title     string            `json:"title,omitempty"`
	Message   string            `json:"message,omitempty"`
	Data      any               `json:"data"`
	Meta      any               `json:"meta,omitempty"`
	Raw       string            `json:"raw,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Code      string            `json:"code,omitempty"`
	Retryable *bool             `json:"retryable,omitempty"`
	Fields    []FieldError      `json:"fields,omitempty"`
}

// FieldError is one invalid input field.
type FieldError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// ErrorBody documents what an error response carries (for OpenAPI); Error and
// its conveniences write exactly these fields.
type ErrorBody struct {
	Status    string       `json:"status" binding:"required,oneof=error" doc:"always error"`
	Title     string       `json:"title,omitempty" doc:"the app that answered"`
	Message   string       `json:"message" binding:"required" doc:"for people; may change"`
	Code      string       `json:"code" binding:"required" doc:"stable, for programs: bad_request, unauthorized, not_found, or an app's own code"`
	Retryable bool         `json:"retryable" binding:"required" doc:"true when the same request may succeed later"`
	Fields    []FieldError `json:"fields,omitempty" doc:"the invalid fields, when the input was invalid"`
}

// PaginationMeta matches the TS CRUD factory pagination shape exactly.
type PaginationMeta struct {
	CurrentPage int   `json:"current_page"`
	NextPage    *int  `json:"next_page"`
	PrevPage    *int  `json:"prev_page"`
	PerPage     int   `json:"per_page"`
	Total       int64 `json:"total"`
}

// ntxOutput implements types.Output and serializes the Envelope directly.
// Bypasses goose's JSONOutput wrapper so clients receive the NTX shape, not a double-wrapped response.
type ntxOutput struct {
	envelope Envelope
	code     int
	headers  map[string]string
}

func (o *ntxOutput) Data() any                  { return o.envelope }
func (o *ntxOutput) Code() int                  { return o.code }
func (o *ntxOutput) Headers() map[string]string { return o.headers }
func (o *ntxOutput) ContentType() string        { return "application/json; charset=utf-8" }

func newOutput(e Envelope, code int) types.Output {
	return &ntxOutput{envelope: e, code: code, headers: map[string]string{}}
}

func paginationMeta(page, perPage int, total int64) PaginationMeta {
	totalPages := int((total + int64(perPage) - 1) / int64(perPage))
	if totalPages < 1 {
		totalPages = 1
	}
	var next, prev *int
	if page < totalPages {
		n := page + 1
		next = &n
	}
	if page > 1 {
		p := page - 1
		prev = &p
	}
	return PaginationMeta{
		CurrentPage: page,
		NextPage:    next,
		PrevPage:    prev,
		PerPage:     perPage,
		Total:       total,
	}
}

// Success returns HTTP 200 with the NTX success envelope.
func Success(data any, title, message string, meta any) types.Output {
	return newOutput(Envelope{Status: "success", Title: title, Message: message, Data: data, Meta: meta}, http.StatusOK)
}

// Created returns HTTP 201 with the NTX success envelope.
func Created(data any, title, message string) types.Output {
	return newOutput(Envelope{Status: "success", Title: title, Message: message, Data: data}, http.StatusCreated)
}

// Paginated returns HTTP 200 with a success envelope and pagination meta.
func Paginated(data any, page, perPage int, total int64, title, message string) types.Output {
	return newOutput(Envelope{
		Status:  "success",
		Title:   title,
		Message: message,
		Data:    data,
		Meta:    paginationMeta(page, perPage, total),
	}, http.StatusOK)
}

// Error returns a JSON error envelope with the given HTTP status code.
// Mirrors TS error() which throws an HttpException carrying the envelope body.
// Its code is the status's (StatusCode); ErrorCode names a more specific one.
func Error(title, message string, code int) types.Output {
	return ErrorCode(title, message, code, StatusCode(code))
}

// ErrorCode is Error with an app's own stable code, such as "username_taken".
func ErrorCode(title, message string, status int, code string, fields ...FieldError) types.Output {
	retry := Retryable(status)
	return newOutput(Envelope{Status: "error", Title: title, Message: message, Data: nil, Code: code, Retryable: &retry, Fields: fields}, status)
}

// Invalid is a 400 naming the fields that are wrong.
func Invalid(title, message string, fields ...FieldError) types.Output {
	return ErrorCode(title, message, http.StatusBadRequest, "invalid", fields...)
}

// StatusCode is the default code for an HTTP status: its text in snake case
// ("not_found", "too_many_requests"), "internal" for any 5xx without a closer name.
func StatusCode(status int) string {
	if status == http.StatusInternalServerError {
		return "internal"
	}
	t := http.StatusText(status)
	if t == "" {
		if status >= 500 {
			return "internal"
		}
		return "error"
	}
	return strings.NewReplacer(" ", "_", "-", "_", "'", "").Replace(strings.ToLower(t))
}

// Retryable says whether the same request can succeed later without changes:
// rate limits, timeouts and server-side failures.
func Retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= 500
}

// NotFound is a 404 error convenience.
func NotFound(title, message string) types.Output {
	return Error(title, message, http.StatusNotFound)
}

// BadRequest is a 400 error convenience.
func BadRequest(title, message string) types.Output {
	return Error(title, message, http.StatusBadRequest)
}

// Conflict is a 409 error convenience.
func Conflict(title, message string) types.Output {
	return Error(title, message, http.StatusConflict)
}

// Unauthorized is a 401 error convenience.
func Unauthorized(title, message string) types.Output {
	return Error(title, message, http.StatusUnauthorized)
}

// Forbidden is a 403 error convenience.
func Forbidden(title, message string) types.Output {
	return Error(title, message, http.StatusForbidden)
}

// InternalServerError is a 500 error convenience.
func InternalServerError(title, message string) types.Output {
	return Error(title, message, http.StatusInternalServerError)
}

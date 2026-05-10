package response

import (
	"net/http"

	"github.com/awesome-goose/goose/types"
)

// Envelope is the standard NTX API response body.
// Matches the TypeScript BaseResponseBody (jsx-*/common/utils/values.ts) shape exactly:
// {"status":"success|error","title":"...","message":"...","data":{...},"meta":{...},"raw":"...","headers":{...}}
type Envelope struct {
	Status  string            `json:"status"`
	Title   string            `json:"title,omitempty"`
	Message string            `json:"message,omitempty"`
	Data    any               `json:"data"`
	Meta    any               `json:"meta,omitempty"`
	Raw     string            `json:"raw,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
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
func Error(title, message string, code int) types.Output {
	return newOutput(Envelope{Status: "error", Title: title, Message: message, Data: nil}, code)
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

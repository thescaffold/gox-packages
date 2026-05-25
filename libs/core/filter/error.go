package filter

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages/libs/core/events"
	"github.com/thescaffold/gox-packages/libs/core/response"
)

// HTTPError is the sentinel error a handler may return to map to a specific
// HTTP status code. The global error middleware translates these to the
// matching response.* envelope.
//
// Use the package-level errors below or wrap them with fmt.Errorf("...: %w", ErrNotFound).
type HTTPError struct {
	Status  int
	Title   string
	Message string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("%d %s", e.Status, e.Message) }

// Sentinel errors for the most common HTTP statuses. The middleware uses
// errors.Is to map them; callers can wrap and add context.
var (
	ErrBadRequest   = &HTTPError{Status: http.StatusBadRequest, Title: "BadRequest", Message: "bad request"}
	ErrUnauthorized = &HTTPError{Status: http.StatusUnauthorized, Title: "Unauthorized", Message: "unauthorized"}
	ErrForbidden    = &HTTPError{Status: http.StatusForbidden, Title: "Forbidden", Message: "forbidden"}
	ErrNotFound     = &HTTPError{Status: http.StatusNotFound, Title: "NotFound", Message: "not found"}
	ErrConflict     = &HTTPError{Status: http.StatusConflict, Title: "Conflict", Message: "conflict"}
)

// SQLErrorMapper takes a SQL driver error and returns a friendly message.
// Provide one when you want the middleware to detect SQL errors and replace
// the raw message (mirrors TS getSqlError(errno|code)).
type SQLErrorMapper func(err error) (handled bool, friendly string)

var (
	// pgSQLStateRe matches the "(SQLSTATE 23505)" suffix GORM appends to wrapped
	// PostgreSQL driver errors.
	pgSQLStateRe = regexp.MustCompile(`SQLSTATE (\w{5})`)
	// mysqlErrnoRe matches the "Error 1062 (23000)" prefix of go-sql-driver MySQL
	// errors — TS reads exception.errno (the numeric code), so we capture that.
	mysqlErrnoRe = regexp.MustCompile(`Error (\d+) \(\d{5}\)`)
)

// DefaultSQLMapper detects a DB driver error by its distinctive signature and
// returns the friendly GetSQLError message — mirroring TS error.filter.ts which
// runs getSqlError(exception.errno ?? exception.code) for every QueryFailedError.
// It recognises PostgreSQL errors (which carry "(SQLSTATE <code>)") and MySQL
// errors (which carry "Error <errno> (<sqlstate>)"), without taking a driver
// dependency. Unknown-but-detected codes still yield the GetSQLError fallback,
// exactly as TS getSqlError does.
func DefaultSQLMapper(err error) (bool, string) {
	if err == nil {
		return false, ""
	}
	msg := err.Error()
	if m := pgSQLStateRe.FindStringSubmatch(msg); len(m) > 1 {
		return true, GetSQLError(m[1])
	}
	if m := mysqlErrnoRe.FindStringSubmatch(msg); len(m) > 1 {
		return true, GetSQLError(m[1])
	}
	return false, ""
}

// SentryHook is called once per captured exception. Wire your Sentry SDK here.
type SentryHook func(err error, stack []byte)

// ErrorMiddleware is a global goose middleware that converts panics and
// returned errors into the NTX response.* envelope. It also forwards every
// captured exception to the configured TrackerService and Sentry hook.
//
// Mirrors ntx-packages/libs/core/src/filters/error.filter.ts.
type ErrorMiddleware struct {
	Tracker      *events.TrackerService
	SQLMapper    SQLErrorMapper
	SentryHook   SentryHook
	DefaultMsg   string
	DefaultTitle string
}

// NewErrorMiddleware constructs an ErrorMiddleware with the given dependencies.
// Pass nil for any field you don't need; sensible defaults apply.
func NewErrorMiddleware(tracker *events.TrackerService) *ErrorMiddleware {
	return &ErrorMiddleware{
		Tracker: tracker,
		// Wire the SQL-error mapper by default so DB constraint/driver errors are
		// translated to friendly messages, matching TS error.filter.ts which
		// always runs getSqlError() on a QueryFailedError.
		SQLMapper:    DefaultSQLMapper,
		DefaultTitle: "Oops",
		DefaultMsg:   "Something went wrong. It's not you, it's us and we are working on fixing it",
	}
}

// Wrap returns a function that runs `next`, catching panics and mapping any
// returned error to a response.* envelope. Use it from your routing layer like:
//
//	wrapped := filter.NewErrorMiddleware(tracker).Wrap(myHandler)
func (m *ErrorMiddleware) Wrap(next func(types.Context) (types.Output, error)) func(types.Context) types.Output {
	return func(ctx types.Context) (out types.Output) {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				pErr := fmt.Errorf("panic: %v", r)
				m.report(pErr, stack)
				out = m.toOutput(pErr)
			}
		}()
		result, err := next(ctx)
		if err != nil {
			m.report(err, debug.Stack())
			return m.toOutput(err)
		}
		return result
	}
}

// toOutput converts an error to a response.* envelope.
func (m *ErrorMiddleware) toOutput(err error) types.Output {
	// HTTPError sentinels
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.Status {
		case http.StatusBadRequest:
			return response.BadRequest(httpErr.Title, httpErr.Message)
		case http.StatusUnauthorized:
			return response.Unauthorized(httpErr.Title, httpErr.Message)
		case http.StatusForbidden:
			return response.Forbidden(httpErr.Title, httpErr.Message)
		case http.StatusNotFound:
			return response.NotFound(httpErr.Title, httpErr.Message)
		case http.StatusConflict:
			return response.Conflict(httpErr.Title, httpErr.Message)
		default:
			return response.Error(httpErr.Title, httpErr.Message, httpErr.Status)
		}
	}

	// SQL errors via configured mapper
	if m.SQLMapper != nil {
		if handled, friendly := m.SQLMapper(err); handled {
			return response.InternalServerError(m.DefaultTitle, friendly)
		}
	}

	// Generic 500
	msg := err.Error()
	if msg == "" || strings.Contains(strings.ToLower(msg), "panic:") {
		msg = m.DefaultMsg
	}
	return response.InternalServerError(m.DefaultTitle, msg)
}

// report logs the exception via tracker and forwards to Sentry, if configured.
func (m *ErrorMiddleware) report(err error, stack []byte) {
	if m.Tracker != nil {
		m.Tracker.Message("apps.common.log.error", map[string]any{
			"exception": err.Error(),
			"trace":     string(stack),
		})
	}
	if m.SentryHook != nil {
		m.SentryHook(err, stack)
	}
}

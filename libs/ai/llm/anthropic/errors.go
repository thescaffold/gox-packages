package anthropic

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/thescaffold/gox-packages/libs/ai/core"
)

const provider = "anthropic"

// mapError turns whatever the SDK or transport returned into a
// *core.ProviderError (or a context error, unchanged, so errors.Is works).
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var pe *core.ProviderError
	if errors.As(err, &pe) {
		return err
	}
	var ae *sdk.Error
	if errors.As(err, &ae) {
		e := &core.ProviderError{Provider: provider, Status: ae.StatusCode, Message: ae.Error(), Cause: err}
		e.Kind = kindForStatus(ae.StatusCode)
		if ae.Response != nil {
			e.RetryAfter = retryAfter(ae.Response.Header)
		}
		return e
	}
	return &core.ProviderError{Provider: provider, Kind: core.KindConnection, Message: err.Error(), Cause: err}
}

func kindForStatus(s int) core.ErrorKind {
	switch {
	case s == 429:
		return core.KindRateLimit
	case s == 404:
		return core.KindNotFound
	case s == 401 || s == 403:
		return core.KindAuth
	case s == 400 || s == 413 || s == 422:
		return core.KindInvalid
	}
	return core.KindStatus
}

func retryAfter(h http.Header) time.Duration {
	if ms := h.Get("Retry-After-Ms"); ms != "" {
		if n, err := strconv.ParseFloat(ms, 64); err == nil && n >= 0 {
			return time.Duration(n * float64(time.Millisecond))
		}
	}
	if s := h.Get("Retry-After"); s != "" {
		if n, err := strconv.ParseFloat(s, 64); err == nil && n >= 0 {
			return time.Duration(n * float64(time.Second))
		}
	}
	return 0
}

// streamError maps an in-stream {"type":"error"} event, where there is no HTTP
// status, by the error type the API names.
func streamError(typ, msg string) *core.ProviderError {
	e := &core.ProviderError{Provider: provider, Message: msg}
	switch typ {
	case "rate_limit_error":
		e.Kind, e.Status = core.KindRateLimit, 429
	case "overloaded_error":
		e.Kind, e.Status = core.KindStatus, 529
	case "invalid_request_error":
		e.Kind, e.Status = core.KindInvalid, 400
	case "authentication_error", "permission_error":
		e.Kind, e.Status = core.KindAuth, 401
	case "not_found_error":
		e.Kind, e.Status = core.KindNotFound, 404
	default: // api_error and anything new: treat as a server fault
		e.Kind, e.Status = core.KindStatus, 500
	}
	return e
}

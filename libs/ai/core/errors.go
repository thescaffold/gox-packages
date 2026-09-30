package core

import (
	"errors"
	"fmt"
	"time"
)

// Sentinels a caller can test with errors.Is.
var (
	ErrNotFound     = errors.New("ai: not found")
	ErrCancelled    = errors.New("ai: cancelled")
	ErrUnauthorized = errors.New("ai: unauthorized")
	ErrValidation   = errors.New("ai: validation failed")
)

// ErrorKind classifies a provider failure; it decides retry and failover.
type ErrorKind string

const (
	KindNotFound   ErrorKind = "not_found"
	KindRateLimit  ErrorKind = "rate_limit"
	KindStatus     ErrorKind = "status"     // any other non-2xx
	KindConnection ErrorKind = "connection" // network, timeout, malformed body
	KindInvalid    ErrorKind = "invalid"    // the request itself was rejected (400)
	KindAuth       ErrorKind = "auth"       // credentials refused (401/403)
)

// ProviderError is a failed provider call.
type ProviderError struct {
	Provider string
	Kind     ErrorKind
	// Status is the HTTP status, 0 for a connection failure.
	Status int
	// RetryAfter is the provider's suggested wait, when it gave one.
	RetryAfter time.Duration
	Message    string
	Cause      error
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("ai: %s %s (status %d): %s", e.Provider, e.Kind, e.Status, e.Message)
}

func (e *ProviderError) Unwrap() error { return e.Cause }

// Retryable reports whether the same request may succeed if repeated: rate
// limits, 5xx responses and connection failures. A rejected request or refused
// credentials will not get better by retrying.
func (e *ProviderError) Retryable() bool {
	switch e.Kind {
	case KindRateLimit, KindConnection:
		return true
	case KindStatus:
		return e.Status >= 500 || e.Status == 408 || e.Status == 409
	}
	return false
}

// Failover reports whether the next model in the fallback chain should be
// tried, because this model or provider is unhealthy rather than the request
// being bad. A bad request would fail identically on the next model.
func (e *ProviderError) Failover() bool {
	switch e.Kind {
	case KindRateLimit, KindConnection, KindNotFound:
		return true
	case KindStatus:
		return e.Status >= 500
	}
	return false
}

// IsRetryable reports whether err is, or wraps, a retryable ProviderError.
func IsRetryable(err error) bool {
	var pe *ProviderError
	return errors.As(err, &pe) && pe.Retryable()
}

// BudgetExceededError: a step, time, token or cost budget ran out.
type BudgetExceededError struct {
	// Budget names which one: "steps", "time", "tokens", "cost".
	Budget string
	Limit  int64
	Used   int64
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("ai: %s budget exceeded (used %d of %d)", e.Budget, e.Used, e.Limit)
}

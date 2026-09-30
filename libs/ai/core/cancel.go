package core

import (
	"context"
	"sync"
)

// CancelToken is cooperative cancellation a long loop checks between steps and
// a driver honours mid-call. The kill switch (TRD §6.14) cancels a run in
// another process by making the host's token report cancelled.
type CancelToken interface {
	// Cancelled reports whether cancellation was requested.
	Cancelled() bool
	// Done is closed when cancellation is requested.
	Done() <-chan struct{}
	// Reason is why, once cancelled; empty before.
	Reason() string
}

// CancelSource owns a token and cancels it.
type CancelSource struct {
	once   sync.Once
	mu     sync.Mutex
	done   chan struct{}
	reason string
}

// NewCancelSource returns a source whose token is not yet cancelled.
func NewCancelSource() *CancelSource { return &CancelSource{done: make(chan struct{})} }

// Cancel requests cancellation. Only the first call's reason is kept.
func (s *CancelSource) Cancel(reason string) {
	s.once.Do(func() {
		s.mu.Lock()
		s.reason = reason
		s.mu.Unlock()
		close(s.done)
	})
}

// Token is the read side.
func (s *CancelSource) Token() CancelToken { return (*token)(s) }

type token CancelSource

func (t *token) Done() <-chan struct{} { return t.done }

func (t *token) Cancelled() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

func (t *token) Reason() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reason
}

// Context derives a context that is cancelled when the token is, so the same
// cancellation reaches an HTTP call. The returned func releases the watcher
// and must be called.
func Context(parent context.Context, t CancelToken) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-t.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

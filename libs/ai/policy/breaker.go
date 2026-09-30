package policy

import (
	"sync"
	"time"
)

// Breaker is a per-key circuit breaker. After Threshold consecutive failures
// the key is open and refuses traffic for Cooldown; then one probe is let
// through (half-open). A success closes it, a failure re-opens it. It is safe
// for concurrent use.
type Breaker struct {
	// Threshold defaults to 5, Cooldown to 30s.
	Threshold int
	Cooldown  time.Duration
	// Now defaults to time.Now.
	Now func() time.Time

	mu sync.Mutex
	m  map[string]*state
}

type state struct {
	failures int
	openedAt time.Time
	open     bool
	probing  bool
}

func (b *Breaker) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Breaker) threshold() int {
	if b.Threshold > 0 {
		return b.Threshold
	}
	return 5
}

func (b *Breaker) cooldown() time.Duration {
	if b.Cooldown > 0 {
		return b.Cooldown
	}
	return 30 * time.Second
}

func (b *Breaker) get(key string) *state {
	if b.m == nil {
		b.m = map[string]*state{}
	}
	s, ok := b.m[key]
	if !ok {
		s = &state{}
		b.m[key] = s
	}
	return s
}

// Allow reports whether a call to key may go ahead. In the half-open state it
// admits exactly one probe until that probe reports.
func (b *Breaker) Allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.get(key)
	if !s.open {
		return true
	}
	if b.now().Sub(s.openedAt) < b.cooldown() || s.probing {
		return false
	}
	s.probing = true
	return true
}

// Success closes the breaker for key.
func (b *Breaker) Success(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.get(key)
	s.failures, s.open, s.probing = 0, false, false
}

// Failure records a failed call; it opens the breaker at the threshold, and a
// failed probe re-opens it for another cooldown.
func (b *Breaker) Failure(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.get(key)
	s.failures++
	if s.open || s.failures >= b.threshold() {
		s.open, s.probing, s.openedAt = true, false, b.now()
	}
}

// Open reports whether key is currently refusing traffic (without consuming a probe).
func (b *Breaker) Open(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.get(key)
	return s.open && (b.now().Sub(s.openedAt) < b.cooldown() || s.probing)
}

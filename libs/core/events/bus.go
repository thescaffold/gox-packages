package events

import (
	"strings"
	"sync"
)

// EventHandler is the callback signature for all bus subscribers.
type EventHandler func(eventType string, payload any)

type subscription struct {
	pattern  string
	segments []string
	handler  EventHandler
}

// Bus is a thread-safe, in-process wildcard pub/sub event bus.
//
// Pattern rules mirror TS EventEmitter2 with `wildcard: true, delimiter: '.'`
// (see ntx-packages/libs/core/src/module/app/app.factory.ts):
//   - "*"   matches exactly one segment
//   - "**"  matches zero or more segments
//
// Example patterns: "apps.*.*.after-insert", "apps.**", "*"
type Bus struct {
	mu   sync.RWMutex
	subs []subscription
}

// NewBus creates an empty Bus.
func NewBus() *Bus {
	return &Bus{}
}

// Subscribe registers handler for all events whose type matches pattern.
// It is safe to call from multiple goroutines.
func (b *Bus) Subscribe(pattern string, handler EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs = append(b.subs, subscription{
		pattern:  pattern,
		segments: strings.Split(pattern, "."),
		handler:  handler,
	})
}

// Publish dispatches payload to all handlers whose pattern matches eventType.
//
// Handlers run SYNCHRONOUSLY in the publishing goroutine, matching the default
// behavior of EventEmitter2.emit() in the TS service. (TS uses emitAsync for
// async dispatch — gox has no equivalent currently.) A slow handler therefore
// blocks the publisher; callers that want concurrency must launch their own
// goroutine around Publish.
func (b *Bus) Publish(eventType string, payload any) {
	b.mu.RLock()
	matched := make([]EventHandler, 0)
	segs := strings.Split(eventType, ".")
	for _, s := range b.subs {
		if matchSegments(s.segments, segs) {
			matched = append(matched, s.handler)
		}
	}
	b.mu.RUnlock()

	for _, h := range matched {
		h(eventType, payload)
	}
}

// matchSegments returns true if the wildcard pattern matches the event segments.
func matchSegments(pattern, event []string) bool {
	return matchAt(pattern, 0, event, 0)
}

func matchAt(pat []string, pi int, ev []string, ei int) bool {
	// Consume any leading ** wildcards eagerly to simplify the hot path.
	for pi < len(pat) && pat[pi] == "**" {
		pi++
		// ** matches zero or more: try skipping zero segments first.
		if matchAt(pat, pi, ev, ei) {
			return true
		}
		// Try skipping each subsequent segment.
		for i := ei; i < len(ev); i++ {
			if matchAt(pat, pi, ev, i+1) {
				return true
			}
		}
		return false
	}

	// Both exhausted → match.
	if pi == len(pat) && ei == len(ev) {
		return true
	}
	// Pattern exhausted but event has more → no match.
	if pi == len(pat) || ei == len(ev) {
		return false
	}

	if pat[pi] == "*" || pat[pi] == ev[ei] {
		return matchAt(pat, pi+1, ev, ei+1)
	}
	return false
}

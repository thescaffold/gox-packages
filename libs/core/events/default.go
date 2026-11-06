package events

import "sync"

// The process's bus. Every CoreModule's tracker publishes on it, so an app's
// subscriptions (RegisterSubscriptions) hear events published by any other
// app in the process — as the TS factory's single EventEmitter2 did. A bus
// made with NewBus is separate (tests, isolated use).
var (
	defaultBus = NewBus()
	regMu      sync.Mutex
	registered = map[string]bool{}
)

// Default returns the process's bus.
func Default() *Bus { return defaultBus }

// RegisterSubscriptions subscribes an app's handlers (its index.go
// Subscriptions map) on the process's bus, once per name: a host calls it for
// each app whose subscriptions it wants live.
func RegisterSubscriptions(name string, subs map[string]EventHandler) {
	regMu.Lock()
	defer regMu.Unlock()
	if registered[name] {
		return
	}
	registered[name] = true
	for pattern, h := range subs {
		defaultBus.Subscribe(pattern, h)
	}
}

package events

import (
	"sync/atomic"
	"testing"
)

// Every module's tracker publishes on the process's bus, so an app's
// subscriptions hear events published by any other app (TS: one shared
// EventEmitter2).
func TestAppSubscriptionsHearEveryTracker(t *testing.T) {
	var heard atomic.Int64
	RegisterSubscriptions("test-app", map[string]EventHandler{
		"apps.test.message.new": func(string, any) { heard.Add(1) },
	})
	// Registering the same app again does not double-deliver.
	RegisterSubscriptions("test-app", map[string]EventHandler{
		"apps.test.message.new": func(string, any) { heard.Add(1) },
	})
	a, b := NewTrackerService(Default()), NewTrackerService(Default())
	a.Message("apps.test.message.new", map[string]any{"from": "a"})
	b.Message("APPS.TEST.MESSAGE.NEW", map[string]any{"from": "b"})
	if heard.Load() != 2 {
		t.Fatalf("heard %d, want 2", heard.Load())
	}
}

func TestHasSubscriber(t *testing.T) {
	b := NewBus()
	b.Subscribe("apps.*.*.after-insert", func(string, any) {})
	if !b.HasSubscriber("apps.db.identityusers.after-insert") {
		t.Fatal("a matching pattern was not found")
	}
	if b.HasSubscriber("apps.db.identityusers.after-update") {
		t.Fatal("a pattern matched an event it does not cover")
	}
}

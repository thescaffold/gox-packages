package tests

import (
	"sync"
	"testing"
	"time"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages-core/events"
)

func TestEvents(t *testing.T) {
	test.NewSuiteRunner(t, &EventsSuite{}).Run()
}

type EventsSuite struct {
	test.Suite
}

// collect returns a handler that appends received event types to a slice.
// The mutex and slice are returned so the test can read them safely.
func collect(mu *sync.Mutex, received *[]string) events.EventHandler {
	return func(eventType string, _ any) {
		mu.Lock()
		*received = append(*received, eventType)
		mu.Unlock()
	}
}

// waitFor spins until the slice has at least n entries or timeout expires.
func waitFor(mu *sync.Mutex, received *[]string, n int) bool {
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(*received)
		mu.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// --- exact match ---

func (s *EventsSuite) TestExactMatch() {
	bus := events.NewBus()
	var mu sync.Mutex
	var received []string
	bus.Subscribe("apps.foo.bar", collect(&mu, &received))
	bus.Publish("apps.foo.bar", nil)
	s.T.Expect(waitFor(&mu, &received, 1)).ToEqual(true)
	mu.Lock()
	s.T.Expect(received[0]).ToEqual("apps.foo.bar")
	mu.Unlock()
}

func (s *EventsSuite) TestExactMatch_NoFalsePositive() {
	bus := events.NewBus()
	var mu sync.Mutex
	var received []string
	bus.Subscribe("apps.foo.bar", collect(&mu, &received))
	bus.Publish("apps.foo.baz", nil)
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	s.T.Expect(len(received)).ToEqual(0)
	mu.Unlock()
}

// --- single-segment wildcard (*) ---

func (s *EventsSuite) TestSingleWildcard_MatchesOneSegment() {
	bus := events.NewBus()
	var mu sync.Mutex
	var received []string
	bus.Subscribe("apps.*.after-insert", collect(&mu, &received))
	bus.Publish("apps.users.after-insert", nil)
	s.T.Expect(waitFor(&mu, &received, 1)).ToEqual(true)
}

func (s *EventsSuite) TestSingleWildcard_NoMatchExtra() {
	bus := events.NewBus()
	var mu sync.Mutex
	var received []string
	bus.Subscribe("apps.*.after-insert", collect(&mu, &received))
	// extra segment — should NOT match
	bus.Publish("apps.users.logs.after-insert", nil)
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	s.T.Expect(len(received)).ToEqual(0)
	mu.Unlock()
}

func (s *EventsSuite) TestMultipleWildcards() {
	bus := events.NewBus()
	var mu sync.Mutex
	var received []string
	// matches *.*.*.after-insert (4 segments, last = "after-insert")
	bus.Subscribe("*.*.*.after-insert", collect(&mu, &received))
	bus.Publish("apps.audit.logs.after-insert", nil)
	s.T.Expect(waitFor(&mu, &received, 1)).ToEqual(true)
}

// --- hash wildcard (#) ---

func (s *EventsSuite) TestHashWildcard_MatchesZeroOrMore() {
	bus := events.NewBus()
	var mu sync.Mutex
	var received []string
	bus.Subscribe("apps.#", collect(&mu, &received))
	bus.Publish("apps.foo.bar.baz", nil)
	bus.Publish("apps.x", nil)
	s.T.Expect(waitFor(&mu, &received, 2)).ToEqual(true)
}

func (s *EventsSuite) TestHashWildcard_StandaloneMatchesAll() {
	bus := events.NewBus()
	var mu sync.Mutex
	var received []string
	bus.Subscribe("#", collect(&mu, &received))
	bus.Publish("anything.at.all", nil)
	s.T.Expect(waitFor(&mu, &received, 1)).ToEqual(true)
}

// --- multiple subscribers ---

func (s *EventsSuite) TestMultipleSubscribers_AllReceive() {
	bus := events.NewBus()
	var mu sync.Mutex
	var received []string
	bus.Subscribe("x.y", collect(&mu, &received))
	bus.Subscribe("x.*", collect(&mu, &received))
	bus.Subscribe("#", collect(&mu, &received))
	bus.Publish("x.y", nil)
	s.T.Expect(waitFor(&mu, &received, 3)).ToEqual(true)
}

// --- payload delivery ---

func (s *EventsSuite) TestPayloadDelivered() {
	bus := events.NewBus()
	done := make(chan any, 1)
	bus.Subscribe("test.event", func(_ string, payload any) {
		done <- payload
	})
	bus.Publish("test.event", map[string]any{"key": "value"})
	select {
	case p := <-done:
		m := p.(map[string]any)
		s.T.Expect(m["key"]).ToEqual("value")
	case <-time.After(200 * time.Millisecond):
		s.T.Expect(false).ToEqual(true) // timeout
	}
}

// --- TrackerService ---

func (s *EventsSuite) TestTrackerService_Message() {
	bus := events.NewBus()
	done := make(chan string, 1)
	bus.Subscribe("apps.foo.bar", func(et string, _ any) { done <- et })

	tracker := events.NewTrackerService(bus)
	tracker.Message("apps.foo.bar", nil)

	select {
	case et := <-done:
		s.T.Expect(et).ToEqual("apps.foo.bar")
	case <-time.After(200 * time.Millisecond):
		s.T.Expect(false).ToEqual(true)
	}
}

func (s *EventsSuite) TestTrackerService_Message_Lowercased() {
	bus := events.NewBus()
	done := make(chan string, 1)
	bus.Subscribe("apps.foo.bar", func(et string, _ any) { done <- et })

	tracker := events.NewTrackerService(bus)
	tracker.Message("APPS.FOO.BAR", nil)

	select {
	case et := <-done:
		s.T.Expect(et).ToEqual("apps.foo.bar")
	case <-time.After(200 * time.Millisecond):
		s.T.Expect(false).ToEqual(true)
	}
}

func (s *EventsSuite) TestTrackerService_Identify_HasUserId() {
	bus := events.NewBus()
	done := make(chan any, 1)
	bus.Subscribe("#", func(_ string, p any) { done <- p })

	tracker := events.NewTrackerService(bus)
	tracker.Identify("user-42", map[string]any{"name": "Alice"})

	select {
	case p := <-done:
		m := p.(map[string]any)
		s.T.Expect(m["userId"]).ToEqual("user-42")
		s.T.Expect(m["name"]).ToEqual("Alice")
	case <-time.After(200 * time.Millisecond):
		s.T.Expect(false).ToEqual(true)
	}
}

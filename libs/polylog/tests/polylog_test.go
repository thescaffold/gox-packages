package tests

import (
	"sync"
	"testing"
	"time"

	test "github.com/awesome-goose/goose/testing"
	coreevents "github.com/thescaffold/gox-packages-core/events"
	"github.com/thescaffold/gox-packages-polylog"
	"github.com/thescaffold/gox-packages-polylog/events"
)

func TestPolylog(t *testing.T) {
	test.NewSuiteRunner(t, &PolylogSuite{}).Run()
}

type PolylogSuite struct {
	test.Suite
}

// helpers ──────────────────────────────────────────────────────────────────────

func newService() (*events.EventsService, *coreevents.Bus) {
	bus := coreevents.NewBus()
	tracker := coreevents.NewTrackerService(bus)
	return events.New(tracker), bus
}

func waitEvent(mu *sync.Mutex, got *[]string, n int) bool {
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		l := len(*got)
		mu.Unlock()
		if l >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// ── EventsService ──────────────────────────────────────────────────────────────

func (s *PolylogSuite) TestIdentify_PublishesEvent() {
	svc, bus := newService()
	var mu sync.Mutex
	var received []string
	bus.Subscribe("apps.common..user.identify", func(et string, _ any) {
		mu.Lock()
		received = append(received, et)
		mu.Unlock()
	})
	svc.Identify("u1", map[string]any{"email": "u@example.com"})
	s.T.Expect(waitEvent(&mu, &received, 1)).ToEqual(true)
	s.T.Expect(len(received)).ToEqual(1)
}

func (s *PolylogSuite) TestTrack_PublishesEvent() {
	svc, bus := newService()
	var mu sync.Mutex
	var received []string
	bus.Subscribe("apps.common..user.login", func(et string, _ any) {
		mu.Lock()
		received = append(received, et)
		mu.Unlock()
	})
	svc.Track("u1", "login", map[string]any{"source": "web"})
	s.T.Expect(waitEvent(&mu, &received, 1)).ToEqual(true)
	s.T.Expect(len(received)).ToEqual(1)
}

func (s *PolylogSuite) TestMessage_PublishesEvent() {
	svc, bus := newService()
	var mu sync.Mutex
	var received []string
	bus.Subscribe("order.created", func(et string, _ any) {
		mu.Lock()
		received = append(received, et)
		mu.Unlock()
	})
	svc.Message("order.created", map[string]any{"id": "o1"})
	s.T.Expect(waitEvent(&mu, &received, 1)).ToEqual(true)
}

func (s *PolylogSuite) TestMessage_LowercasesEventType() {
	svc, bus := newService()
	var mu sync.Mutex
	var received []string
	bus.Subscribe("order.shipped", func(et string, _ any) {
		mu.Lock()
		received = append(received, et)
		mu.Unlock()
	})
	svc.Message("ORDER.SHIPPED", nil)
	s.T.Expect(waitEvent(&mu, &received, 1)).ToEqual(true)
}

func (s *PolylogSuite) TestIdentify_NilAttrs_DoesNotPanic() {
	svc, _ := newService()
	svc.Identify("u1", nil)
}

func (s *PolylogSuite) TestTrack_NilAttrs_DoesNotPanic() {
	svc, _ := newService()
	svc.Track("u1", "click", nil)
}

// ── PolylogModule ──────────────────────────────────────────────────────────────

func (s *PolylogSuite) TestRegister_ReturnsModule() {
	m := polylog.Register(polylog.PolylogConfig{})
	s.T.Expect(m == nil).ToEqual(false)
}

func (s *PolylogSuite) TestDeclarations_NonEmpty() {
	m := polylog.Register(polylog.PolylogConfig{})
	s.T.Expect(len(m.Declarations()) > 0).ToEqual(true)
}

func (s *PolylogSuite) TestExports_MatchDeclarations() {
	m := polylog.Register(polylog.PolylogConfig{})
	s.T.Expect(len(m.Exports())).ToEqual(len(m.Declarations()))
}

func (s *PolylogSuite) TestImports_Nil() {
	m := polylog.Register(polylog.PolylogConfig{})
	s.T.Expect(m.Imports() == nil).ToEqual(true)
}

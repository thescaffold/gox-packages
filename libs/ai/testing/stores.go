package aitesting

import (
	"context"
	"sync"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
)

// Memory implements every persistence and metering port in memory, for tests
// of anything that consumes them. It is safe for concurrent use.
//
// The value itself is a CredentialStore, UsageSink and MessageStore. The
// ResponseCache, AgentStepStore and AgentToolCallStore come from Cache, Steps
// and ToolCalls, because their method names would collide.
type Memory struct {
	mu       sync.Mutex
	Creds    map[string]map[string]string
	messages map[string][]core.Message
	steps    map[string][]core.StepRecord
	calls    map[string][]core.ToolCallRecord
	status   map[string]StatusChange
	usage    []core.UsageEvent
	usageIDs map[string]bool
	cache    map[string]cached
	// Now is the clock; defaults to time.Now.
	Now func() time.Time
}

// StatusChange is the last status set for a run.
type StatusChange struct {
	Status core.RunStatus
	Detail string
}

type cached struct {
	r       core.ChatResponse
	expires time.Time
}

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{
		Creds:    map[string]map[string]string{},
		messages: map[string][]core.Message{},
		steps:    map[string][]core.StepRecord{},
		calls:    map[string][]core.ToolCallRecord{},
		status:   map[string]StatusChange{},
		usageIDs: map[string]bool{},
		cache:    map[string]cached{},
	}
}

func (m *Memory) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Get implements core.CredentialStore.
func (m *Memory) Get(_ context.Context, ref string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.Creds[ref]
	if !ok {
		return nil, core.ErrNotFound
	}
	out := make(map[string]string, len(c))
	for k, v := range c {
		out[k] = v
	}
	return out, nil
}

// Record implements core.UsageSink, idempotent on the event ID.
func (m *Memory) Record(_ context.Context, e core.UsageEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.ID != "" {
		if m.usageIDs[e.ID] {
			return nil
		}
		m.usageIDs[e.ID] = true
	}
	m.usage = append(m.usage, e)
	return nil
}

// Usage returns every recorded event.
func (m *Memory) Usage() []core.UsageEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]core.UsageEvent(nil), m.usage...)
}

// CacheGet implements the read side of core.ResponseCache.
func (m *Memory) CacheGet(_ context.Context, key string) (*core.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cache[key]
	if !ok || (!c.expires.IsZero() && !m.now().Before(c.expires)) {
		return nil, nil
	}
	r := c.r
	return &r, nil
}

// CacheSet implements the write side of core.ResponseCache.
func (m *Memory) CacheSet(_ context.Context, key string, r *core.ChatResponse, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var exp time.Time
	if ttl > 0 {
		exp = m.now().Add(ttl)
	}
	m.cache[key] = cached{r: *r, expires: exp}
	return nil
}

// Cache adapts Memory to core.ResponseCache (whose methods are Get and Set,
// names already taken by the credential port on this type).
func (m *Memory) Cache() core.ResponseCache { return memCache{m} }

type memCache struct{ m *Memory }

func (c memCache) Get(ctx context.Context, k string) (*core.ChatResponse, error) {
	return c.m.CacheGet(ctx, k)
}
func (c memCache) Set(ctx context.Context, k string, r *core.ChatResponse, ttl time.Duration) error {
	return c.m.CacheSet(ctx, k, r, ttl)
}

// Append implements core.MessageStore. It validates, stamps the time, and
// never mutates what it stored.
func (m *Memory) Append(_ context.Context, runID string, msg core.Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if msg.At.IsZero() {
		msg.At = m.now()
	}
	m.messages[runID] = append(m.messages[runID], msg)
	return nil
}

// List implements core.MessageStore, in append order, as a copy.
func (m *Memory) List(_ context.Context, runID string) ([]core.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]core.Message(nil), m.messages[runID]...), nil
}

// Steps returns the adapters for the step and tool-call ports, whose method
// names would otherwise collide with the message store's List.
func (m *Memory) Steps() core.AgentStepStore         { return memSteps{m} }
func (m *Memory) ToolCalls() core.AgentToolCallStore { return memCalls{m} }

// Status returns the last status set for a run.
func (m *Memory) Status(runID string) (StatusChange, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.status[runID]
	return s, ok
}

type memSteps struct{ m *Memory }

func (s memSteps) Save(_ context.Context, r core.StepRecord) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	list := s.m.steps[r.RunID]
	for i := range list {
		if list[i].ID == r.ID { // upsert: a resumed run re-saves a step
			list[i] = r
			return nil
		}
	}
	s.m.steps[r.RunID] = append(list, r)
	return nil
}

func (s memSteps) List(_ context.Context, runID string) ([]core.StepRecord, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	return append([]core.StepRecord(nil), s.m.steps[runID]...), nil
}

func (s memSteps) SetStatus(_ context.Context, runID string, st core.RunStatus, detail string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.m.status[runID] = StatusChange{Status: st, Detail: detail}
	return nil
}

type memCalls struct{ m *Memory }

func (s memCalls) Save(_ context.Context, r core.ToolCallRecord) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	list := s.m.calls[r.RunID]
	for i := range list {
		if list[i].ID == r.ID {
			list[i] = r
			return nil
		}
	}
	s.m.calls[r.RunID] = append(list, r)
	return nil
}

func (s memCalls) List(_ context.Context, runID string) ([]core.ToolCallRecord, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	return append([]core.ToolCallRecord(nil), s.m.calls[runID]...), nil
}

// Package durable bridges the in-process event bus (core/events) to goose's
// Postgres job queue (modules/queues), so a subscriber gets every event at
// least once even when it is slow, failing, or not running when the event
// is published.
//
// Each durable subscription has its own queue ("events.<name>"). An event
// that matches its pattern becomes one job there; workers run the handler
// with retries and backoff, and a job that is in progress when the process
// dies is picked up again once the queue recovers stale jobs
// (queues.RecoverStaleJobs, or the queue module's CleanupInterval). A job
// that fails every attempt stays in QueueJobs with status "failed" — the
// dead letters — until an operator retries it (Queue.Retry).
//
// Delivery is at-least-once, so handlers must be idempotent: Delivery.ID is
// the same for every attempt of one event to one subscription. Dedupe on it.
//
// Two ways to feed it:
//
//	bridge.Attach(bus)            // events published on the bus are enqueued
//
//	relay := &outbox.Relay{DB: db, Bus: bridge.Relay(bus)}
//	                              // transactional: an event exists iff its change committed,
//	                              // and an event the bridge cannot enqueue is retried by the outbox
//
// Attach is durable from the moment Publish returns; a crash inside Publish
// itself can lose the event. The outbox path closes that gap.
package durable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/awesome-goose/goose/modules/queues"
	"github.com/google/uuid"
	"github.com/thescaffold/gox-packages/libs/core/events"
)

// Queue is what the bridge needs from goose's *queues.Queue.
type Queue interface {
	Push(queueName string, jobName string, data any, config *queues.JobConfig) (*queues.QueueJob, error)
	Process(handler *queues.JobHandler)
}

// Delivery is one event as a durable subscriber receives it.
type Delivery struct {
	// ID is stable across every attempt of this event to this subscription:
	// the handler's dedupe key.
	ID string `json:"id"`
	// EventID is the event's own ID: the outbox event's ID when the event
	// came through the outbox, otherwise one the bridge gave it.
	EventID      string          `json:"eventId"`
	Subscription string          `json:"subscription"`
	Event        string          `json:"event"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	PublishedAt  time.Time       `json:"publishedAt"`
	// JobID is the queue job carrying it (to find it among the dead letters).
	JobID string `json:"-"`
}

// Decode unmarshals the payload into v.
func (d Delivery) Decode(v any) error {
	if len(d.Payload) == 0 {
		return errors.New("durable: empty payload")
	}
	return json.Unmarshal(d.Payload, v)
}

// Handler processes one delivery. An error retries it (up to the
// subscription's retry limit); nil completes it.
type Handler func(ctx context.Context, d Delivery) error

// JobName is the job name of every delivery job.
const JobName = "deliver"

// QueueName is the queue a subscription's deliveries wait in.
func QueueName(subscription string) string { return "events." + subscription }

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)

// SubOption tunes one subscription.
type SubOption func(*subscription)

// WithRetry sets how many times a failed delivery is retried, and the delay
// before the first retry in milliseconds (doubling each time, capped at a
// minute). Default 5 retries from 1000 ms.
func WithRetry(limit, delayMs int) SubOption {
	return func(s *subscription) { s.retryLimit, s.retryDelay = limit, delayMs }
}

// WithWorkers sets the worker pool (default 1..4).
func WithWorkers(min, max int) SubOption {
	return func(s *subscription) { s.minWorkers, s.maxWorkers = min, max }
}

// WithPollInterval sets how often idle workers look for work, in ms (default 1000).
func WithPollInterval(ms int) SubOption { return func(s *subscription) { s.pollMs = ms } }

// WithTimeout bounds one attempt, in ms (default 30000).
func WithTimeout(ms int) SubOption { return func(s *subscription) { s.timeoutMs = ms } }

type subscription struct {
	name, pattern string
	segments      []string
	handler       Handler

	retryLimit, retryDelay int
	minWorkers, maxWorkers int
	pollMs, timeoutMs      int
	started                bool
}

// Bridge connects the bus to the queue. Build one per process with New.
type Bridge struct {
	q   Queue
	log *slog.Logger

	mu       sync.RWMutex
	subs     map[string]*subscription
	order    []string
	started  bool
	attached map[*events.Bus]bool

	// relaying holds the IDs of outbox events Relay is forwarding to the bus
	// right now, so an attached handler does not enqueue them a second time.
	relaying sync.Map
}

// New returns a bridge over q (goose's *queues.Queue). logger may be nil.
func New(q Queue, logger *slog.Logger) *Bridge {
	if logger == nil {
		logger = slog.Default()
	}
	return &Bridge{q: q, log: logger, subs: map[string]*subscription{}, attached: map[*events.Bus]bool{}}
}

// Subscribe registers a durable subscriber: every event matching pattern
// (core/events wildcards: "*" one segment, "**" any) is delivered to handler
// at least once. name identifies the subscription and its queue; it must be
// stable across deploys (renaming it abandons the old queue's backlog).
// Subscribing after Start starts its workers at once.
func (b *Bridge) Subscribe(name, pattern string, handler Handler, opts ...SubOption) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("durable: invalid subscription name %q (lowercase letters, digits, . _ -)", name)
	}
	if pattern == "" || handler == nil {
		return errors.New("durable: pattern and handler are required")
	}
	s := &subscription{
		name: name, pattern: pattern, segments: strings.Split(pattern, "."), handler: handler,
		retryLimit: 5, retryDelay: 1000, minWorkers: 1, maxWorkers: 4, pollMs: 1000, timeoutMs: 30000,
	}
	for _, o := range opts {
		o(s)
	}
	b.mu.Lock()
	if _, dup := b.subs[name]; dup {
		b.mu.Unlock()
		return fmt.Errorf("durable: subscription %q already registered", name)
	}
	b.subs[name] = s
	b.order = append(b.order, name)
	start := b.started
	b.mu.Unlock()
	if start {
		b.process(s)
	}
	return nil
}

// Start runs the workers of every subscription. Call it once the queue
// tables exist (after the sql module booted); goose's Process also waits for
// sql.Ready.
func (b *Bridge) Start() {
	b.mu.Lock()
	b.started = true
	var subs []*subscription
	for _, n := range b.order {
		subs = append(subs, b.subs[n])
	}
	b.mu.Unlock()
	for _, s := range subs {
		b.process(s)
	}
}

func (b *Bridge) process(s *subscription) {
	b.mu.Lock()
	if s.started {
		b.mu.Unlock()
		return
	}
	s.started = true
	b.mu.Unlock()
	h := queues.NewHandler(QueueName(s.name), JobName, func(ctx context.Context, job *queues.QueueJob) (any, error) {
		d, err := queues.GetJobData[Delivery](job)
		if err != nil {
			return nil, fmt.Errorf("durable: bad delivery job %s: %w", job.Id, err)
		}
		d.JobID = job.Id
		return nil, s.handler(ctx, d)
	}).WithMinWorkers(s.minWorkers).WithMaxWorkers(s.maxWorkers).WithPollInterval(s.pollMs).WithTimeout(s.timeoutMs).WithExponentialBackoff()
	b.q.Process(h)
}

// Attach makes events published on bus durable: every one that matches a
// subscription is enqueued for it, as it is published. Subscriptions added
// later are covered too. Attaching a bus twice does nothing.
func (b *Bridge) Attach(bus *events.Bus) {
	b.mu.Lock()
	if b.attached[bus] {
		b.mu.Unlock()
		return
	}
	b.attached[bus] = true
	b.mu.Unlock()
	// One listener for the whole bus, so one event gets one EventID across
	// every subscription it matches.
	bus.Subscribe("**", func(eventType string, payload any) {
		if id, ok := eventID(payload); ok {
			if _, relayed := b.relaying.Load(id); relayed {
				return // Relay already enqueued it
			}
		}
		if err := b.PublishChecked(eventType, payload); err != nil {
			b.log.Error("durable: could not enqueue event", "event", eventType, "err", err)
		}
	})
}

// Publish enqueues eventType for every matching subscription, logging any
// failure. It makes the bridge usable where an events.Bus-like publisher is
// expected; prefer PublishChecked when the caller can retry.
func (b *Bridge) Publish(eventType string, payload any) {
	if err := b.PublishChecked(eventType, payload); err != nil {
		b.log.Error("durable: could not enqueue event", "event", eventType, "err", err)
	}
}

// PublishChecked enqueues eventType for every matching subscription and
// reports the first failure (after trying them all). A retry after a partial
// failure enqueues the subscriptions that succeeded again: at-least-once,
// which Delivery.ID lets handlers absorb.
func (b *Bridge) PublishChecked(eventType string, payload any) error {
	subs := b.matching(eventType)
	if len(subs) == 0 {
		return nil
	}
	id, ok := eventID(payload)
	if !ok {
		id = uuid.NewString()
	}
	var first error
	for _, s := range subs {
		if err := b.enqueue(s, id, eventType, payload); err != nil && first == nil {
			first = fmt.Errorf("durable: enqueue %s for %s: %w", eventType, s.name, err)
		}
	}
	return first
}

// Relay returns the publisher to give an outbox.Relay as its Bus: each event
// is enqueued for the durable subscriptions (a failure makes the outbox retry
// it), then published on bus for the in-process subscribers. bus may be nil.
func (b *Bridge) Relay(bus *events.Bus) *Relayer { return &Relayer{b: b, bus: bus} }

// Relayer is the outbox publisher Relay returns (an outbox.CheckedPublisher).
type Relayer struct {
	b   *Bridge
	bus *events.Bus
}

// Publish is PublishChecked with the error logged.
func (r *Relayer) Publish(eventType string, payload any) {
	if err := r.PublishChecked(eventType, payload); err != nil {
		r.b.log.Error("durable: relay failed", "event", eventType, "err", err)
	}
}

// PublishChecked enqueues, then forwards to the bus.
func (r *Relayer) PublishChecked(eventType string, payload any) error {
	if err := r.b.PublishChecked(eventType, payload); err != nil {
		return err
	}
	if r.bus == nil {
		return nil
	}
	if id, ok := eventID(payload); ok {
		r.b.relaying.Store(id, struct{}{})
		defer r.b.relaying.Delete(id)
	}
	r.bus.Publish(eventType, payload)
	return nil
}

func (b *Bridge) matching(eventType string) []*subscription {
	segs := strings.Split(eventType, ".")
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []*subscription
	for _, n := range b.order {
		if s := b.subs[n]; events.MatchSegments(s.segments, segs) {
			out = append(out, s)
		}
	}
	return out
}

func (b *Bridge) enqueue(s *subscription, eventID, eventType string, payload any) error {
	d := Delivery{Subscription: s.name, Event: eventType, EventID: eventID, PublishedAt: time.Now().UTC()}
	d.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(s.name+"\x00"+d.EventID)).String()
	if raw, ok := rawJSON(payload); ok {
		d.Payload = raw
	} else if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		d.Payload = b
	}
	_, err := b.q.Push(QueueName(s.name), JobName, d, &queues.JobConfig{RetryLimit: s.retryLimit, RetryDelay: s.retryDelay, TimeoutMs: s.timeoutMs})
	return err
}

func eventID(payload any) (string, bool) {
	if p, ok := payload.(interface{ EventID() string }); ok && p.EventID() != "" {
		return p.EventID(), true
	}
	return "", false
}

func rawJSON(payload any) (json.RawMessage, bool) {
	if p, ok := payload.(interface{ RawJSON() json.RawMessage }); ok && len(p.RawJSON()) > 0 {
		return p.RawJSON(), true
	}
	return nil, false
}

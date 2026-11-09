// Package dbevents publishes the rows written through Goose's sql.Entity as
// events on a scaffold bus, as the TS repository did on every save, update and
// delete: apps.db.<table>.after-insert|after-update|after-delete, the table name
// lower-cased (apps.db.identityusers.after-insert).
//
// It is what makes the libraries' apps.db.* Subscriptions fire (identity's
// user, workspace and device handlers, capital's wallet set-up, audit's log).
// It lives apart from package events so that events stays free of the
// database layer.
package dbevents

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/awesome-goose/goose/modules/sql"
	"github.com/thescaffold/gox-packages/libs/core/events"
)

// The suffixes of the three events.
const (
	AfterInsert = "after-insert"
	AfterUpdate = "after-update"
	AfterDelete = "after-delete"
)

// Name is the event a write to table publishes: Name("IdentityUsers",
// AfterInsert) is "apps.db.identityusers.after-insert".
func Name(table, after string) string {
	return "apps.db." + strings.ToLower(table) + "." + after
}

var (
	mu      sync.Mutex
	streams = map[*events.Bus]*stream{}
)

// Publish publishes every row written through a Goose sql.Entity in this
// process (Insert, Upsert, Update, Delete; see sql.ObserveChanges) on bus.
// Opt-in: a host calls it once at start-up, with events.Default() (nil means
// that) so the handlers events.RegisterSubscriptions made live hear it. A
// second call for the same bus adds nothing.
//
// The payload is the TS one: {entityName (the table), entityId, action
// ("create"|"update"|"delete"), meta (the row as its JSON fields), group
// (GROUP_NAME), service (APP_NAME), context}. context is empty, since a write
// carries no request: a subscriber that needs the actor (audit) skips these.
// A table nobody subscribes to costs a pattern match per write, nothing more.
//
// Events are delivered in write order, on a goroutine of their own, after the
// write (after the commit, inside sql.Entity.Tx and sql.Query.Tx). So a slow,
// failing or panicking subscriber never holds up or breaks the request that
// wrote the row, and a subscriber that writes cannot deadlock against the
// writer's still-open transaction. Only if thousands of events are waiting is
// one delivered on the writer's goroutine instead. Rows written with gorm
// directly are not reported.
//
// stop removes the observer and returns once every queued event has been
// delivered (tests use it to wait).
func Publish(bus *events.Bus) (stop func()) {
	if bus == nil {
		bus = events.Default()
	}
	mu.Lock()
	defer mu.Unlock()
	if s, ok := streams[bus]; ok {
		return s.stop
	}
	s := newStream(bus)
	s.unlisten = sql.ObserveChanges(s.observe)
	streams[bus] = s
	return s.stop
}

type stream struct {
	bus      *events.Bus
	mu       sync.RWMutex // closed vs. a send racing stop
	closed   bool
	queue    chan change
	done     chan struct{}
	unlisten func()
	once     sync.Once
}

type change struct {
	name    string
	payload map[string]any
}

func newStream(bus *events.Bus) *stream {
	s := &stream{bus: bus, queue: make(chan change, 4096), done: make(chan struct{}), unlisten: func() {}}
	go func() {
		defer close(s.done)
		for c := range s.queue {
			s.deliver(c)
		}
	}()
	return s
}

func (s *stream) observe(ev sql.ChangeEvent) {
	after := afterFor(ev.Action)
	if after == "" {
		return
	}
	name := Name(ev.Table, after)
	if !s.bus.HasSubscriber(name) {
		return
	}
	meta := toMeta(ev.Record)
	id, _ := meta["id"].(string)
	if id == "" && ev.Query == "id = ?" && len(ev.Args) == 1 {
		if v, ok := ev.Args[0].(string); ok && v != "" {
			id = v
			meta["id"] = v
		}
	}
	c := change{name: name, payload: map[string]any{
		"entityName": ev.Table,
		"entityId":   id,
		"action":     ev.Action,
		"meta":       meta,
		"group":      os.Getenv("GROUP_NAME"),
		"service":    os.Getenv("APP_NAME"),
		"context":    map[string]any{},
	}}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.queue <- c:
	default:
		// The queue is full (or a subscriber is writing from the delivery
		// goroutine itself while it is): deliver here rather than block or drop.
		s.deliver(c)
	}
}

func (s *stream) stop() {
	s.once.Do(func() {
		s.unlisten()
		mu.Lock()
		if streams[s.bus] == s {
			delete(streams, s.bus)
		}
		mu.Unlock()
		s.mu.Lock()
		s.closed = true
		close(s.queue)
		s.mu.Unlock()
		<-s.done
	})
}

// deliver keeps the delivery goroutine alive when a subscriber panics.
func (s *stream) deliver(c change) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "dbevents: a subscriber of %s panicked: %v\n", c.name, r)
		}
	}()
	s.bus.Publish(c.name, c.payload)
}

func afterFor(action string) string {
	switch action {
	case sql.ChangeCreate:
		return AfterInsert
	case sql.ChangeUpdate:
		return AfterUpdate
	case sql.ChangeDelete:
		return AfterDelete
	}
	return ""
}

// toMeta is the row as its JSON fields (the names subscribers read: id,
// userId, clientId, workspaceId, ...). A field the entity keeps out of JSON
// (json:"-", such as a password hash) stays out.
func toMeta(record any) map[string]any {
	out := map[string]any{}
	if record == nil {
		return out
	}
	b, err := json.Marshal(record)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

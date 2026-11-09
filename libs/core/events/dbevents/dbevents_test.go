package dbevents

import (
	"sync"
	"testing"

	"github.com/awesome-goose/goose/modules/sql"
	"github.com/thescaffold/gox-packages/libs/core/events"
)

type row struct {
	Id     string  `json:"id"`
	UserId string  `json:"userId"`
	Secret *string `json:"-"`
}

type heard struct {
	mu   sync.Mutex
	got  []string
	last map[string]any
}

func (h *heard) handler(name string, payload any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.got = append(h.got, name)
	h.last, _ = payload.(map[string]any)
}

func TestWritesBecomeDBEventsInOrder(t *testing.T) {
	bus := events.NewBus()
	h := &heard{}
	bus.Subscribe("apps.db.*.after-insert", h.handler)
	bus.Subscribe("apps.db.identityusers.after-update", h.handler)
	bus.Subscribe("apps.db.identityusers.after-delete", h.handler)

	s := newStream(bus)
	secret := "hash"
	s.observe(sql.ChangeEvent{Table: "IdentityUsers", Action: sql.ChangeCreate, Record: &row{Id: "u1", UserId: "u1", Secret: &secret}})
	s.observe(sql.ChangeEvent{Table: "IdentityUsers", Action: sql.ChangeUpdate, Record: &row{Id: "u1"}})
	s.observe(sql.ChangeEvent{Table: "Unwatched", Action: sql.ChangeUpdate, Record: &row{Id: "x"}})
	s.observe(sql.ChangeEvent{Table: "IdentityUsers", Action: sql.ChangeDelete, Record: &row{}, Query: "id = ?", Args: []any{"u1"}})
	s.stop()
	s.stop() // idempotent

	want := []string{"apps.db.identityusers.after-insert", "apps.db.identityusers.after-update", "apps.db.identityusers.after-delete"}
	if len(h.got) != len(want) {
		t.Fatalf("heard %v, want %v", h.got, want)
	}
	for i := range want {
		if h.got[i] != want[i] {
			t.Fatalf("heard %v, want %v", h.got, want)
		}
	}
	// The delete by id names the row even though the record was not loaded.
	if h.last["entityId"] != "u1" || h.last["action"] != "delete" || h.last["entityName"] != "IdentityUsers" {
		t.Fatalf("delete payload %+v", h.last)
	}
}

func TestPayloadIsTheTSShapeAndKeepsSecretsOut(t *testing.T) {
	bus := events.NewBus()
	h := &heard{}
	bus.Subscribe("apps.db.identityusers.after-insert", h.handler)
	t.Setenv("GROUP_NAME", "g")
	t.Setenv("APP_NAME", "svc")
	s := newStream(bus)
	secret := "hash"
	s.observe(sql.ChangeEvent{Table: "IdentityUsers", Action: sql.ChangeCreate, Record: &row{Id: "u1", UserId: "u1", Secret: &secret}})
	s.stop()

	p := h.last
	meta, _ := p["meta"].(map[string]any)
	if p["entityId"] != "u1" || p["action"] != "create" || p["group"] != "g" || p["service"] != "svc" || meta["userId"] != "u1" {
		t.Fatalf("payload %+v", p)
	}
	if _, ok := meta["Secret"]; ok {
		t.Fatal("a json:\"-\" field reached the event")
	}
	if _, ok := p["context"].(map[string]any); !ok {
		t.Fatal("context missing")
	}
}

func TestAPanickingSubscriberDoesNotStopDelivery(t *testing.T) {
	bus := events.NewBus()
	h := &heard{}
	bus.Subscribe("apps.db.a.after-insert", func(string, any) { panic("boom") })
	bus.Subscribe("apps.db.b.after-insert", h.handler)
	s := newStream(bus)
	s.observe(sql.ChangeEvent{Table: "A", Action: sql.ChangeCreate, Record: &row{Id: "1"}})
	s.observe(sql.ChangeEvent{Table: "B", Action: sql.ChangeCreate, Record: &row{Id: "2"}})
	s.stop()
	if len(h.got) != 1 {
		t.Fatalf("heard %v after a panicking subscriber", h.got)
	}
}

func TestPublishIsOncePerBus(t *testing.T) {
	bus := events.NewBus()
	stop1 := Publish(bus)
	stop2 := Publish(bus)
	mu.Lock()
	n := len(streams)
	mu.Unlock()
	stop2()
	stop1()
	if n != 1 {
		t.Fatalf("%d streams for one bus", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(streams) != 0 {
		t.Fatal("stop left the stream registered")
	}
}

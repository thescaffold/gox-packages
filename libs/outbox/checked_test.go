package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

type checkedBus struct {
	bus
	refuse int // refuse this many PublishChecked calls
	plain  int // Publish calls (must stay 0)
}

func (b *checkedBus) Publish(name string, payload any) { b.plain++ }

func (b *checkedBus) PublishChecked(name string, payload any) error {
	if b.refuse > 0 {
		b.refuse--
		return errors.New("queue unavailable")
	}
	b.bus.Publish(name, payload)
	return nil
}

// A CheckedPublisher's refusal is a failed delivery: the event is retried, not
// marked published, and order holds. Delivery exposes its ID and body to
// consumers that do not import this package.
func TestACheckedPublisherCanRefuseAnEvent(t *testing.T) {
	db := testDB(t)
	_ = db.Transaction(func(tx *gorm.DB) error {
		add(t, tx, 1, "a")
		add(t, tx, 2, "b")
		return nil
	})
	now := time.Now()
	b := &checkedBus{refuse: 1}
	r := &Relay{DB: db, Bus: b, Now: func() time.Time { return now }}

	n, err := r.Drain(context.Background())
	if err != nil || n != 0 || len(b.got) != 0 {
		t.Fatalf("refused drain: n=%d err=%v got=%d", n, err, len(b.got))
	}
	var e Event
	db.Where("name = ?", "a").First(&e)
	if e.Attempts != 1 || e.PublishedAt != nil || e.LastError == nil || *e.LastError != "queue unavailable" {
		t.Fatalf("refused event: %+v", e)
	}

	now = now.Add(time.Hour) // past the backoff
	if n, err := r.Drain(context.Background()); err != nil || n != 2 {
		t.Fatalf("second drain: n=%d err=%v", n, err)
	}
	if len(b.names) != 2 || b.names[0] != "a" || b.names[1] != "b" || b.plain != 0 {
		t.Fatalf("names %v, plain Publish calls %d", b.names, b.plain)
	}
	var id interface{ EventID() string } = b.got[0]
	if id.EventID() != b.got[0].ID || string(b.got[0].RawJSON()) != `{"n":1}` {
		t.Fatalf("accessors: %q %s", id.EventID(), b.got[0].RawJSON())
	}
}

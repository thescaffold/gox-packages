package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// sqlite has no jsonb; AutoMigrate maps the column to a compatible type.
	db.Migrator().DropTable(&Event{})
	if err := db.Exec(`CREATE TABLE "EventOutbox" (
		id varchar(36) PRIMARY KEY, seq integer, name varchar(255) NOT NULL, payload text,
		created_at datetime, published_at datetime, attempts integer DEFAULT 0,
		next_attempt_at datetime, last_error text, parked boolean DEFAULT false)`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

type bus struct {
	got      []Delivery
	names    []string
	failNext int
	panicOn  string
}

func (b *bus) Publish(name string, payload any) {
	if b.panicOn == name {
		panic("boom")
	}
	b.names = append(b.names, name)
	b.got = append(b.got, payload.(Delivery))
}

// seq is not auto-assigned by sqlite for a non-PK column, so number rows explicitly.
func add(t *testing.T, tx *gorm.DB, seq int64, name string) string {
	t.Helper()
	id, err := Add(tx, name, map[string]any{"n": seq})
	if err != nil {
		t.Fatal(err)
	}
	tx.Model(&Event{}).Where("id = ?", id).Update("seq", seq)
	return id
}

func TestEventExistsOnlyIfTheTransactionCommits(t *testing.T) {
	db := testDB(t)
	_ = db.Transaction(func(tx *gorm.DB) error {
		add(t, tx, 1, "wallet.credited")
		return errors.New("rollback")
	})
	var n int64
	db.Model(&Event{}).Count(&n)
	if n != 0 {
		t.Fatalf("a rolled-back change left %d event(s)", n)
	}
	_ = db.Transaction(func(tx *gorm.DB) error { add(t, tx, 2, "wallet.credited"); return nil })
	db.Model(&Event{}).Count(&n)
	if n != 1 {
		t.Fatalf("a committed change must leave its event, got %d", n)
	}
}

func TestRelayPublishesInOrderExactlyOnce(t *testing.T) {
	db := testDB(t)
	_ = db.Transaction(func(tx *gorm.DB) error {
		add(t, tx, 1, "a.one")
		add(t, tx, 2, "a.two")
		add(t, tx, 3, "a.three")
		return nil
	})
	b := &bus{}
	r := &Relay{DB: db, Bus: b}
	n, err := r.Drain(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	if b.names[0] != "a.one" || b.names[1] != "a.two" || b.names[2] != "a.three" {
		t.Fatalf("out of order: %v", b.names)
	}
	if n, _ := r.Drain(context.Background()); n != 0 || len(b.got) != 3 {
		t.Fatalf("published again: %d %d", n, len(b.got))
	}
	if b.got[0].ID == "" || b.got[0].Payload["n"] != float64(1) {
		t.Fatalf("delivery: %+v", b.got[0])
	}
}

func TestAFailingSubscriberIsRetriedWithBackoffAndOrderIsKept(t *testing.T) {
	db := testDB(t)
	_ = db.Transaction(func(tx *gorm.DB) error { add(t, tx, 1, "x.bad"); add(t, tx, 2, "x.good"); return nil })
	now := time.Now()
	b := &bus{panicOn: "x.bad"}
	r := &Relay{DB: db, Bus: b, Now: func() time.Time { return now }, Backoff: func(int) time.Duration { return time.Minute }}

	n, _ := r.Drain(context.Background())
	if n != 0 || len(b.names) != 0 {
		t.Fatalf("a later event must not overtake a failing one: %d %v", n, b.names)
	}
	var e Event
	db.Where("name = ?", "x.bad").First(&e)
	if e.Attempts != 1 || e.LastError == nil {
		t.Fatalf("%+v", e)
	}
	if n, _ := r.Drain(context.Background()); n != 0 {
		t.Fatal("retried before the backoff elapsed")
	}
	now = now.Add(2 * time.Minute)
	b.panicOn = ""
	n, _ = r.Drain(context.Background())
	if n != 2 || b.names[0] != "x.bad" || b.names[1] != "x.good" {
		t.Fatalf("%d %v", n, b.names)
	}
}

func TestEventsAreParkedAfterMaxAttemptsAndCanBeUnparked(t *testing.T) {
	db := testDB(t)
	_ = db.Transaction(func(tx *gorm.DB) error { add(t, tx, 1, "y.bad"); return nil })
	now := time.Now()
	b := &bus{panicOn: "y.bad"}
	r := &Relay{DB: db, Bus: b, MaxAttempts: 2, Now: func() time.Time { return now }, Backoff: func(int) time.Duration { return 0 }}
	r.Drain(context.Background())
	r.Drain(context.Background())
	var e Event
	db.First(&e)
	if !e.Parked || e.Attempts != 2 {
		t.Fatalf("%+v", e)
	}
	b.panicOn = ""
	if n, _ := r.Drain(context.Background()); n != 0 {
		t.Fatal("a parked event was published")
	}
	if err := Unpark(db, e.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if n, _ := r.Drain(context.Background()); n != 1 {
		t.Fatal("an unparked event should publish")
	}
}

func TestAddRejectsAnEmptyName(t *testing.T) {
	if _, err := Add(testDB(t), "", nil); err == nil {
		t.Fatal("empty event name accepted")
	}
}

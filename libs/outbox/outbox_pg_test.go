package outbox

import (
	"context"
	"os"
	"sync"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type lockedBus struct {
	mu  sync.Mutex
	ids map[string]int
}

func (b *lockedBus) Publish(_ string, p any) {
	b.mu.Lock()
	b.ids[p.(Delivery).ID]++
	b.mu.Unlock()
}

// Two relays draining at once must publish every event exactly once
// (FOR UPDATE SKIP LOCKED). Needs Postgres: set OUTBOX_TEST_DB_URL.
func TestConcurrentRelaysPublishEachEventOnce(t *testing.T) {
	dsn := os.Getenv("OUTBOX_TEST_DB_URL")
	if dsn == "" {
		t.Skip("OUTBOX_TEST_DB_URL not set; skipping Postgres test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Migrator().DropTable(&Event{})
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	defer db.Migrator().DropTable(&Event{})

	const total = 300
	if err := db.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < total; i++ {
			if _, err := Add(tx, "load.event", map[string]any{"i": i}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	bus := &lockedBus{ids: map[string]int{}}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &Relay{DB: db, Bus: bus, Batch: 20}
			for {
				n, err := r.Drain(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()

	if len(bus.ids) != total {
		t.Fatalf("published %d distinct events, want %d", len(bus.ids), total)
	}
	for id, n := range bus.ids {
		if n != 1 {
			t.Fatalf("event %s published %d times", id, n)
		}
	}
	var pending int64
	db.Model(&Event{}).Where("published_at IS NULL").Count(&pending)
	if pending != 0 {
		t.Fatalf("%d left unpublished", pending)
	}
}

// Migrate runs at every boot, so it must be safe to run again on an existing table.
func TestMigrateIsIdempotentOnPostgres(t *testing.T) {
	dsn := os.Getenv("OUTBOX_TEST_DB_URL")
	if dsn == "" {
		t.Skip("OUTBOX_TEST_DB_URL not set; skipping Postgres test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := Migrate(db); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if _, err := Add(db, "x.y", map[string]any{"a": 1}); err != nil {
		t.Fatalf("the table is not usable: %v", err)
	}
}

// Package outbox is a transactional outbox: an event is written in the same
// database transaction as the change that caused it, then a relay publishes it
// to the in-process bus. An event therefore exists if and only if its change
// committed, and a crash between commit and publish loses nothing (delivery is
// at-least-once; consumers dedupe on the event ID).
//
// It is opt-in and independent of core/events: the Bus is unchanged, and any
// type with Publish(eventType string, payload any) can be the target.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Event is one outbox row.
type Event struct {
	ID            string          `gorm:"primaryKey;type:varchar(36)" json:"id"`
	Seq           int64           `gorm:"autoIncrement;uniqueIndex" json:"seq"`
	Name          string          `gorm:"type:varchar(255);not null" json:"name"`
	Payload       json.RawMessage `gorm:"type:jsonb" json:"payload"`
	CreatedAt     time.Time       `json:"createdAt"`
	PublishedAt   *time.Time      `gorm:"index" json:"publishedAt,omitempty"`
	Attempts      int             `json:"attempts"`
	NextAttemptAt time.Time       `gorm:"index" json:"nextAttemptAt"`
	LastError     *string         `gorm:"type:text" json:"lastError,omitempty"`
	// Parked rows exceeded MaxAttempts and wait for an operator.
	Parked bool `gorm:"index" json:"parked"`
}

func (Event) TableName() string { return "EventOutbox" }

// Migrate creates or updates the outbox table.
func Migrate(db *gorm.DB) error { return db.AutoMigrate(&Event{}) }

// Add records an event inside the caller's transaction (tx must be the
// transaction that makes the change). Rolling the transaction back removes it.
func Add(tx *gorm.DB, name string, payload any) (string, error) {
	if name == "" {
		return "", errors.New("outbox: event name is required")
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("outbox: payload: %w", err)
	}
	e := Event{ID: uuid.NewString(), Name: name, Payload: b, CreatedAt: time.Now(), NextAttemptAt: time.Now()}
	if err := tx.Create(&e).Error; err != nil {
		return "", err
	}
	return e.ID, nil
}

// Publisher is satisfied by core/events.Bus.
type Publisher interface {
	Publish(eventType string, payload any)
}

// Delivery is what a subscriber receives as payload: the event ID (for
// de-duplication) and the decoded body.
type Delivery struct {
	ID      string
	Payload map[string]any
	Raw     json.RawMessage
}

// Relay publishes pending events in order.
type Relay struct {
	DB          *gorm.DB
	Bus         Publisher
	Batch       int                             // rows per drain (default 100)
	MaxAttempts int                             // park after this many failures (default 10)
	Backoff     func(attempt int) time.Duration // default 1s * 2^(attempt-1), capped at 5m
	Now         func() time.Time
}

func (r *Relay) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Relay) batch() int {
	if r.Batch > 0 {
		return r.Batch
	}
	return 100
}

func (r *Relay) maxAttempts() int {
	if r.MaxAttempts > 0 {
		return r.MaxAttempts
	}
	return 10
}

func (r *Relay) backoff(attempt int) time.Duration {
	if r.Backoff != nil {
		return r.Backoff(attempt)
	}
	d := time.Second << min(attempt-1, 8)
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

// Drain publishes up to one batch of due events, in order, and returns how many were
// published. Rows are claimed with FOR UPDATE SKIP LOCKED where the database
// supports it, so several relays can run without double publishing.
func (r *Relay) Drain(ctx context.Context) (int, error) {
	published := 0
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []Event
		now := r.now()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("published_at IS NULL AND parked = ?", false).
			Order("seq ASC").Limit(r.batch()).Find(&rows).Error; err != nil {
			return err
		}
		for _, e := range rows {
			// An event waiting out its backoff holds back everything after it,
			// so a later event never overtakes an earlier one. Only parked
			// events are skipped (they need an operator).
			if e.NextAttemptAt.After(now) {
				break
			}
			if perr := r.publish(e); perr != nil {
				e.Attempts++
				msg := perr.Error()
				upd := map[string]any{"attempts": e.Attempts, "last_error": msg}
				if e.Attempts >= r.maxAttempts() {
					upd["parked"] = true
				} else {
					upd["next_attempt_at"] = now.Add(r.backoff(e.Attempts))
				}
				if err := tx.Model(&Event{}).Where("id = ?", e.ID).Updates(upd).Error; err != nil {
					return err
				}
				// Keep order: do not publish later events past a failing one.
				break
			}
			t := now
			if err := tx.Model(&Event{}).Where("id = ?", e.ID).Updates(map[string]any{"published_at": t, "last_error": nil}).Error; err != nil {
				return err
			}
			published++
		}
		return nil
	})
	return published, err
}

// publish hands one event to the bus; a panicking subscriber is a failure.
func (r *Relay) publish(e Event) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("subscriber panicked: %v", rec)
		}
	}()
	var body map[string]any
	_ = json.Unmarshal(e.Payload, &body)
	r.Bus.Publish(e.Name, Delivery{ID: e.ID, Payload: body, Raw: e.Payload})
	return nil
}

// Run drains on an interval until ctx ends.
func (r *Relay) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		for {
			n, err := r.Drain(ctx)
			if err != nil || n < r.batch() {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Unpark makes a parked event eligible again (operator action).
func Unpark(db *gorm.DB, id string) error {
	return db.Model(&Event{}).Where("id = ?", id).
		Updates(map[string]any{"parked": false, "attempts": 0, "next_attempt_at": time.Now()}).Error
}

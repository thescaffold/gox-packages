# outbox

A transactional outbox for Goose services (opt-in; `core/events.Bus` is unchanged).

An event is written **in the same transaction** as the change that caused it, then a relay publishes it to the bus. So an event exists if and only if its change committed, and a crash between commit and publish loses nothing. Delivery is at-least-once: consumers de-duplicate on `Delivery.ID`.

```go
// in the transaction that changes state
err := db.Transaction(func(tx *gorm.DB) error {
    if err := tx.Create(&txn).Error; err != nil { return err }
    _, err := outbox.Add(tx, "wallet.credited", payload)
    return err
})

// once, at startup
_ = outbox.Migrate(db)
go (&outbox.Relay{DB: db, Bus: bus}).Run(ctx, time.Second)

// subscribers receive an outbox.Delivery
bus.Subscribe("wallet.*", func(name string, p any) { d := p.(outbox.Delivery); ... })
```

- Events are published in order. An event waiting out its retry backoff holds back later ones, so none overtakes it.
- A panicking subscriber is a failure: retried with exponential backoff (1s doubling, 5m cap), parked after `MaxAttempts` (10). Parked events are skipped, not blocking; `outbox.Unpark(db, id)` re-queues one.
- Several relays can run at once: rows are claimed `FOR UPDATE SKIP LOCKED` (Postgres test: `OUTBOX_TEST_DB_URL`).

package durable_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/awesome-goose/goose/modules/queues"
	gsql "github.com/awesome-goose/goose/modules/sql"
	"github.com/thescaffold/gox-packages/libs/core/events"
	"github.com/thescaffold/gox-packages/libs/core/events/durable"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Real Postgres, real goose queue: DURABLE_TEST_DB_URL, e.g.
// postgres://postgres@localhost:5432/envelope_cloud_w1c?sslmode=disable
var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("DURABLE_TEST_DB_URL")
	if dsn != "" {
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			panic(err)
		}
		for _, t := range []string{"QueueLogs", "QueueJobs", "QueueQueues"} {
			db.Exec(`DROP TABLE IF EXISTS "` + t + `" CASCADE`)
		}
		if err := db.AutoMigrate(&queues.QueueQueue{}, &queues.QueueJob{}, &queues.QueueLog{}); err != nil {
			panic(err)
		}
		testDB = db
	}
	os.Exit(m.Run())
}

func newQueue(t *testing.T) *queues.Queue {
	t.Helper()
	if testDB == nil {
		t.Skip("DURABLE_TEST_DB_URL not set — skipping (needs a real Postgres)")
	}
	q := queues.NewQueue(testDB, nil)
	t.Cleanup(q.Shutdown)
	return q
}

// recorder collects deliveries, safe for the queue's workers.
type recorder struct {
	mu  sync.Mutex
	got []durable.Delivery
}

func (r *recorder) add(d durable.Delivery) {
	r.mu.Lock()
	r.got = append(r.got, d)
	r.mu.Unlock()
}

func (r *recorder) snapshot() []durable.Delivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]durable.Delivery(nil), r.got...)
}

func (r *recorder) wait(t *testing.T, n int) []durable.Delivery {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := r.snapshot(); len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("got %d deliveries, want %d", len(r.snapshot()), n)
	return nil
}

func fast() []durable.SubOption {
	return []durable.SubOption{durable.WithPollInterval(20), durable.WithRetry(3, 10)}
}

func jobs(t *testing.T, sub, status string) []queues.QueueJob {
	t.Helper()
	q := queues.NewQueue(testDB, nil)
	js, err := q.ListJobs(durable.QueueName(sub), status, 100)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func TestAttachedBusEventsReachEachSubscriptionOnce(t *testing.T) {
	q := newQueue(t)
	bus := events.NewBus()
	b := durable.New(q, nil)
	var tasks, audit recorder
	_ = b.Subscribe("t1-tasks", "apps.tasks.*", func(ctx context.Context, d durable.Delivery) error { tasks.add(d); return nil }, fast()...)
	_ = b.Subscribe("t1-audit", "apps.**", func(ctx context.Context, d durable.Delivery) error { audit.add(d); return nil }, fast()...)
	b.Attach(bus)
	b.Attach(bus) // twice: no double enqueue
	b.Start()

	bus.Publish("apps.tasks.created", map[string]any{"id": "t-1"})
	bus.Publish("apps.mail.sent", map[string]any{"id": "m-1"})

	got := tasks.wait(t, 1)
	all := audit.wait(t, 2)
	time.Sleep(200 * time.Millisecond)
	if n := len(tasks.snapshot()); n != 1 {
		t.Fatalf("tasks got %d deliveries, want exactly 1", n)
	}
	if n := len(audit.snapshot()); n != 2 {
		t.Fatalf("audit got %d deliveries, want exactly 2", n)
	}
	var body map[string]string
	if err := got[0].Decode(&body); err != nil || body["id"] != "t-1" || got[0].Event != "apps.tasks.created" || got[0].JobID == "" {
		t.Fatalf("delivery %+v body %v err %v", got[0], body, err)
	}
	// one event, two subscriptions: same EventID, different delivery IDs
	var sameEvent durable.Delivery
	for _, d := range all {
		if d.Event == "apps.tasks.created" {
			sameEvent = d
		}
	}
	if sameEvent.EventID != got[0].EventID || sameEvent.ID == got[0].ID {
		t.Fatalf("ids: tasks %+v audit %+v", got[0], sameEvent)
	}
}

func TestAFailingHandlerIsRetriedWithTheSameDeliveryID(t *testing.T) {
	q := newQueue(t)
	bus := events.NewBus()
	b := durable.New(q, nil)
	var mu sync.Mutex
	var ids []string
	var ok recorder
	_ = b.Subscribe("t2-flaky", "x.*", func(ctx context.Context, d durable.Delivery) error {
		mu.Lock()
		ids = append(ids, d.ID)
		n := len(ids)
		mu.Unlock()
		if n < 3 {
			return errors.New("downstream unavailable")
		}
		ok.add(d)
		return nil
	}, fast()...)
	b.Attach(bus)
	b.Start()
	bus.Publish("x.y", "payload")
	ok.wait(t, 1)
	mu.Lock()
	defer mu.Unlock()
	if len(ids) != 3 || ids[0] != ids[1] || ids[1] != ids[2] {
		t.Fatalf("attempt ids %v: want three attempts with one ID", ids)
	}
}

// The point of the bridge: events published while the subscriber is down
// wait for it, and a new process with the same subscription name gets them.
func TestEventsPublishedWhileTheSubscriberIsDownAreDeliveredAfterRestart(t *testing.T) {
	q := newQueue(t)
	bus := events.NewBus()
	before := durable.New(q, nil)
	_ = before.Subscribe("t3-workflow", "wf.**", func(context.Context, durable.Delivery) error {
		t.Error("the stopped subscriber ran")
		return nil
	}, fast()...)
	before.Attach(bus) // enqueues, but never Start()ed: the subscriber is down
	for i := 0; i < 3; i++ {
		bus.Publish("wf.trigger", map[string]int{"n": i})
	}
	if n := len(jobs(t, "t3-workflow", queues.JobStatusNew)); n != 3 {
		t.Fatalf("%d jobs waiting, want 3", n)
	}

	// "restart": a new bridge and queue (another process)
	after := durable.New(newQueue(t), nil)
	var rec recorder
	_ = after.Subscribe("t3-workflow", "wf.**", func(ctx context.Context, d durable.Delivery) error { rec.add(d); return nil }, fast()...)
	after.Start()
	got := rec.wait(t, 3)
	seen := map[int]bool{}
	for _, d := range got {
		var v map[string]int
		_ = d.Decode(&v)
		seen[v["n"]] = true
	}
	if len(seen) != 3 {
		t.Fatalf("got %v", seen)
	}
}

// A job that was running when its process died is delivered again once stale
// jobs are recovered.
func TestAnInterruptedDeliveryIsRedelivered(t *testing.T) {
	q := newQueue(t)
	b := durable.New(q, nil)
	var rec recorder
	_ = b.Subscribe("t4-crash", "c.*", func(ctx context.Context, d durable.Delivery) error { rec.add(d); return nil }, fast()...)
	if err := b.PublishChecked("c.x", map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	// a worker in another process popped it, then the process died
	job, err := q.Pop(durable.QueueName("t4-crash"), durable.JobName)
	if err != nil || job == nil {
		t.Fatalf("pop: %v %v", job, err)
	}
	if n, err := queues.RecoverStaleJobs(&gsql.Db{DB: testDB}, 0); err != nil || n < 1 {
		t.Fatalf("recover: %d %v", n, err)
	}
	b.Start()
	if got := rec.wait(t, 1); got[0].JobID != job.Id {
		t.Fatalf("redelivered job %s, want %s", got[0].JobID, job.Id)
	}
}

func TestAHandlerThatAlwaysFailsEndsAsADeadLetter(t *testing.T) {
	q := newQueue(t)
	b := durable.New(q, nil)
	var mu sync.Mutex
	attempts := 0
	_ = b.Subscribe("t5-dead", "d.*", func(context.Context, durable.Delivery) error {
		mu.Lock()
		attempts++
		mu.Unlock()
		return errors.New("always")
	}, durable.WithPollInterval(20), durable.WithRetry(2, 5))
	b.Start()
	_ = b.PublishChecked("d.x", nil)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(jobs(t, "t5-dead", queues.JobStatusFailed)) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(jobs(t, "t5-dead", queues.JobStatusFailed)); n != 1 {
		t.Fatalf("%d failed jobs, want 1", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 3 {
		t.Fatalf("%d attempts, want 3 (1 + 2 retries)", attempts)
	}
}

// outboxDelivery stands in for outbox.Delivery (same accessors).
type outboxDelivery struct {
	ID  string
	Raw json.RawMessage
}

func (d outboxDelivery) EventID() string          { return d.ID }
func (d outboxDelivery) RawJSON() json.RawMessage { return d.Raw }

type failingQueue struct {
	durable.Queue
	fail bool
}

func (f *failingQueue) Push(qn, jn string, data any, c *queues.JobConfig) (*queues.QueueJob, error) {
	if f.fail {
		return nil, errors.New("database down")
	}
	return f.Queue.Push(qn, jn, data, c)
}

// The outbox path: Relay enqueues for durable subscribers (refusing, so the
// outbox retries, when it cannot), then publishes in process — and an
// attached bus does not enqueue the same event twice.
func TestRelayEnqueuesThenPublishesAndNeverTwice(t *testing.T) {
	fq := &failingQueue{Queue: newQueue(t)}
	bus := events.NewBus()
	b := durable.New(fq, nil)
	var durableGot recorder
	_ = b.Subscribe("t6-search", "apps.**", func(ctx context.Context, d durable.Delivery) error { durableGot.add(d); return nil }, fast()...)
	b.Attach(bus)
	var inProcess []string
	bus.Subscribe("apps.**", func(name string, _ any) { inProcess = append(inProcess, name) })
	relay := b.Relay(bus)
	ev := outboxDelivery{ID: "11111111-1111-1111-1111-111111111111", Raw: json.RawMessage(`{"doc":"d1"}`)}

	fq.fail = true
	if err := relay.PublishChecked("apps.files.updated", ev); err == nil {
		t.Fatal("a failed enqueue was reported as delivered")
	}
	if len(inProcess) != 0 {
		t.Fatal("published in process although the durable enqueue failed")
	}
	fq.fail = false
	if err := relay.PublishChecked("apps.files.updated", ev); err != nil {
		t.Fatal(err)
	}
	b.Start()
	got := durableGot.wait(t, 1)
	time.Sleep(200 * time.Millisecond)
	if n := len(durableGot.snapshot()); n != 1 {
		t.Fatalf("durable subscriber got %d deliveries, want 1 (attached bus must not re-enqueue)", n)
	}
	var doc map[string]string
	if err := got[0].Decode(&doc); got[0].EventID != ev.ID || err != nil || doc["doc"] != "d1" {
		t.Fatalf("delivery %+v", got[0])
	}
	if len(inProcess) != 1 {
		t.Fatalf("in-process subscribers got %d, want 1", len(inProcess))
	}
	// a redelivery of the same outbox event has the same delivery ID
	_ = relay.PublishChecked("apps.files.updated", ev)
	again := durableGot.wait(t, 2)
	if again[1].ID != again[0].ID {
		t.Fatalf("delivery IDs differ for one outbox event: %s %s", again[0].ID, again[1].ID)
	}
}

func TestSubscribeValidates(t *testing.T) {
	b := durable.New(nil, nil)
	h := func(context.Context, durable.Delivery) error { return nil }
	for _, name := range []string{"", "Upper", "has space", "../x"} {
		if err := b.Subscribe(name, "a.*", h); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	if err := b.Subscribe("ok", "", h); err == nil {
		t.Error("empty pattern accepted")
	}
	if err := b.Subscribe("ok", "a.*", h); err != nil {
		t.Fatal(err)
	}
	if err := b.Subscribe("ok", "a.*", h); err == nil {
		t.Error("duplicate name accepted")
	}
}

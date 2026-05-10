package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	test "github.com/awesome-goose/goose/testing"
	corehttp "github.com/thescaffold/gox-packages/libs/core/http"
	"github.com/thescaffold/gox-packages/libs/polylog"
	"github.com/thescaffold/gox-packages/libs/polylog/events"
)

func TestPolylog(t *testing.T) {
	test.NewSuiteRunner(t, &PolylogSuite{}).Run()
}

type PolylogSuite struct {
	test.Suite
}

// scaffoldRecorder stubs /apps/polylog/ingest/batch.
type scaffoldRecorder struct {
	*httptest.Server
	mu          sync.Mutex
	requests    int32
	failTimes   int32
	authHeader  string
	receivedAll []map[string]any
}

func newScaffoldRecorder(failTimes int32) *scaffoldRecorder {
	r := &scaffoldRecorder{failTimes: failTimes}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt32(&r.requests, 1)
		r.mu.Lock()
		r.authHeader = req.Header.Get("Authorization")
		var body struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		r.receivedAll = append(r.receivedAll, body.Items...)
		r.mu.Unlock()

		if atomic.LoadInt32(&r.failTimes) > 0 {
			atomic.AddInt32(&r.failTimes, -1)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success"})
	}))
	return r
}

func (r *scaffoldRecorder) ItemCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.receivedAll)
}

// ── EventsService payload shape ──────────────────────────────────────────────

func (s *PolylogSuite) TestIdentify_EnqueuesIdentifyEnvelope() {
	q := events.NewQueue("src-1")
	svc := events.New(q)
	svc.Identify("u1", map[string]any{"email": "u@example.com"}, nil)

	s.T.Expect(q.Len()).ToEqual(1)
	items := q.Pop(1)
	s.T.Expect(string(items[0].Category)).ToEqual("event")
	s.T.Expect(items[0].Type).ToEqual("user.identify")
	payload, _ := items[0].Payload.(map[string]any)
	s.T.Expect(payload["type"]).ToEqual("identify")
	s.T.Expect(payload["id"]).ToEqual("u1")
	attrs, _ := payload["attributes"].(map[string]any)
	s.T.Expect(attrs["email"]).ToEqual("u@example.com")
	s.T.Expect(items[0].EntityID).ToEqual("src-1")
	s.T.Expect(string(items[0].EntityName)).ToEqual("source")
}

func (s *PolylogSuite) TestTrack_EnqueuesTrackEnvelope() {
	q := events.NewQueue("src-1")
	svc := events.New(q)
	svc.Track("u1", "login", map[string]any{"source": "web"}, nil)

	items := q.Pop(1)
	s.T.Expect(items[0].Type).ToEqual("login")
	payload, _ := items[0].Payload.(map[string]any)
	s.T.Expect(payload["type"]).ToEqual("track")
	s.T.Expect(payload["id"]).ToEqual("u1")
	attrs, _ := payload["attributes"].(map[string]any)
	s.T.Expect(attrs["source"]).ToEqual("web")
}

func (s *PolylogSuite) TestMessage_SpreadsAttributes_NotWrapped() {
	q := events.NewQueue("src-1")
	svc := events.New(q)
	svc.Message("order.created", map[string]any{"id": "o1", "amount": 99}, nil)

	items := q.Pop(1)
	s.T.Expect(items[0].Type).ToEqual("order.created")
	payload, ok := items[0].Payload.(map[string]any)
	s.T.Expect(ok).ToEqual(true)
	// payload IS the spread attributes — no "type" or "id" wrapper
	s.T.Expect(payload["id"]).ToEqual("o1")
	s.T.Expect(payload["amount"]).ToEqual(99)
	_, hasTypeKey := payload["type"]
	s.T.Expect(hasTypeKey).ToEqual(false)
}

func (s *PolylogSuite) TestIdentify_NilAttrs_DoesNotPanic() {
	q := events.NewQueue("src")
	svc := events.New(q)
	svc.Identify("u1", nil, nil)
	s.T.Expect(q.Len()).ToEqual(1)
}

// ── Queue ─────────────────────────────────────────────────────────────────────

func (s *PolylogSuite) TestQueue_Push_AssignsDefaultEntity() {
	q := events.NewQueue("src-X")
	q.Push(events.Item{Category: events.CategoryEvent, Type: "x"})
	items := q.Pop(1)
	s.T.Expect(items[0].EntityID).ToEqual("src-X")
	s.T.Expect(string(items[0].EntityName)).ToEqual("source")
}

func (s *PolylogSuite) TestQueue_Pop_RespectsLimit() {
	q := events.NewQueue("src")
	for i := 0; i < 5; i++ {
		q.Push(events.Item{Category: events.CategoryEvent, Type: "e"})
	}
	popped := q.Pop(2)
	s.T.Expect(len(popped)).ToEqual(2)
	s.T.Expect(q.Len()).ToEqual(3)
}

// ── Flusher ───────────────────────────────────────────────────────────────────

func (s *PolylogSuite) TestFlusher_PostsBatchToServer() {
	srv := newScaffoldRecorder(0)
	defer srv.Close()

	q := events.NewQueue("src-1")
	q.Push(events.Item{Category: events.CategoryEvent, Type: "user.identify",
		Payload: map[string]any{"type": "identify", "id": "u1"}})

	f := events.NewFlusher(events.FlusherConfig{
		Server:     srv.URL,
		Credential: "the-token",
		Batch: events.BatchConfig{
			Interval: 20 * time.Millisecond,
			Backoff:  10 * time.Millisecond,
			Limit:    3,
		},
	}, q, corehttp.New(""))
	f.Run()
	defer f.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if srv.ItemCount() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.T.Expect(srv.ItemCount() >= 1).ToEqual(true)
	s.T.Expect(srv.authHeader).ToEqual("bearer the-token")
}

func (s *PolylogSuite) TestFlusher_Retries_OnFailure() {
	srv := newScaffoldRecorder(2) // first 2 requests fail, then succeed
	defer srv.Close()

	q := events.NewQueue("src-1")
	q.Push(events.Item{Category: events.CategoryEvent, Type: "x"})

	f := events.NewFlusher(events.FlusherConfig{
		Server:     srv.URL,
		Credential: "tok",
		Batch: events.BatchConfig{
			Interval: 10 * time.Millisecond,
			Backoff:  10 * time.Millisecond,
			Limit:    5,
		},
	}, q, corehttp.New(""))
	f.Run()
	defer f.Stop()

	// Wait for the third attempt (the one expected to succeed after 2 failures).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&srv.requests) >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.T.Expect(srv.ItemCount() >= 1).ToEqual(true)
	s.T.Expect(atomic.LoadInt32(&srv.requests) >= 3).ToEqual(true)
}

func (s *PolylogSuite) TestFlusher_StopsAfterRetryLimit() {
	srv := newScaffoldRecorder(100) // always fail
	defer srv.Close()

	q := events.NewQueue("src-1")
	q.Push(events.Item{Category: events.CategoryEvent, Type: "x"})

	f := events.NewFlusher(events.FlusherConfig{
		Server:     srv.URL,
		Credential: "tok",
		Batch: events.BatchConfig{
			Interval: 10 * time.Millisecond,
			Backoff:  5 * time.Millisecond,
			Limit:    2,
		},
	}, q, corehttp.New(""))
	f.Run()
	// Wait for the flusher to give up
	time.Sleep(300 * time.Millisecond)

	requestsAtGiveUp := atomic.LoadInt32(&srv.requests)
	// After give-up: subsequent ticks must NOT hit the server
	time.Sleep(200 * time.Millisecond)
	s.T.Expect(atomic.LoadInt32(&srv.requests)).ToEqual(requestsAtGiveUp)
	f.Stop()
}

// ── PolylogModule ─────────────────────────────────────────────────────────────

func (s *PolylogSuite) TestRegister_ValidConfig_ReturnsModule() {
	m := polylog.Register(polylog.PolylogConfig{
		Server: "http://example.test", Credential: "tok", SourceId: "src",
		Batch: events.BatchConfig{Interval: time.Hour, Backoff: time.Hour, Limit: 1},
	})
	s.T.Expect(m == nil).ToEqual(false)
	defer m.Stop()
}

func (s *PolylogSuite) TestRegister_MissingServer_Panics() {
	defer func() { s.T.Expect(recover() == nil).ToEqual(false) }()
	polylog.Register(polylog.PolylogConfig{Credential: "t", SourceId: "s"})
}

func (s *PolylogSuite) TestRegister_MissingSourceId_Panics() {
	defer func() { s.T.Expect(recover() == nil).ToEqual(false) }()
	polylog.Register(polylog.PolylogConfig{Server: "http://x", Credential: "t"})
}

func (s *PolylogSuite) TestDeclarations_HasQueueServiceFlusher() {
	m := polylog.Register(polylog.PolylogConfig{
		Server: "http://example.test", Credential: "tok", SourceId: "src",
		Batch: events.BatchConfig{Interval: time.Hour, Backoff: time.Hour, Limit: 1},
	})
	defer m.Stop()
	s.T.Expect(len(m.Declarations())).ToEqual(3)
}

func (s *PolylogSuite) TestExports_MatchDeclarations() {
	m := polylog.Register(polylog.PolylogConfig{
		Server: "http://example.test", Credential: "tok", SourceId: "src",
		Batch: events.BatchConfig{Interval: time.Hour, Backoff: time.Hour, Limit: 1},
	})
	defer m.Stop()
	s.T.Expect(len(m.Exports())).ToEqual(len(m.Declarations()))
}

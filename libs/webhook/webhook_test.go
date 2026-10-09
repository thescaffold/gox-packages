package webhook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var t0 = time.Unix(1_800_000_000, 0)

func TestSignVerifyRoundTrip(t *testing.T) {
	body := []byte(`{"id":"evt_1","object":"event"}`)
	h := Sign("whsec_a", t0, body)
	if err := Verify("whsec_a", h, body, t0.Add(time.Second), 0); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsTamperWrongSecretReplayAndGarbage(t *testing.T) {
	body := []byte(`{"amount":500000}`)
	h := Sign("whsec_a", t0, body)
	for name, tc := range map[string]struct {
		secret, header string
		body           []byte
		now            time.Time
		want           error
	}{
		"tampered body": {"whsec_a", h, []byte(`{"amount":900000}`), t0, ErrSignature},
		"wrong secret":  {"whsec_b", h, body, t0, ErrSignature},
		"replayed":      {"whsec_a", h, body, t0.Add(10 * time.Minute), ErrTimestamp},
		"from future":   {"whsec_a", h, body, t0.Add(-10 * time.Minute), ErrTimestamp},
		"garbage":       {"whsec_a", "nonsense", body, t0, ErrMalformedHeader},
		"no signature":  {"whsec_a", "t=1800000000", body, t0, ErrMalformedHeader},
	} {
		if err := Verify(tc.secret, tc.header, tc.body, tc.now, 0); err != tc.want {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}

func TestVerifyAcceptsAnyOfSeveralSignaturesDuringRotation(t *testing.T) {
	body := []byte("x")
	old, cur := Sign("old", t0, body), Sign("new", t0, body)
	combined := cur + ",v1=" + strings.Split(old, "v1=")[1]
	if Verify("old", combined, body, t0, 0) != nil || Verify("new", combined, body, t0, 0) != nil {
		t.Fatal("either secret must verify while rotating")
	}
}

func fast() *Sender {
	return &Sender{Header: "Legger-Signature", Backoff: func(int) time.Duration { return time.Millisecond }, Sleep: func(context.Context, time.Duration) {}}
}

func TestDeliverSignsAndSendsTheEventHeaders(t *testing.T) {
	var got http.Header
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	res := fast().Deliver(context.Background(), Request{URL: srv.URL, Secret: "whsec", EventID: "evt_7", EventName: "wallet.credited", Body: []byte(`{"a":1}`)})
	if !res.Delivered || len(res.Attempts) != 1 {
		t.Fatal(res)
	}
	if got.Get("X-Event-Id") != "evt_7" || got.Get("X-Event-Name") != "wallet.credited" || got.Get("X-Delivery-Attempt") != "1" {
		t.Fatalf("%v", got)
	}
	if err := Verify("whsec", got.Get("Legger-Signature"), body, time.Now(), 0); err != nil {
		t.Fatalf("receiver could not verify: %v", err)
	}
}

func TestDeliverRetriesTransientFailuresThenSucceeds(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	res := fast().Deliver(context.Background(), Request{URL: srv.URL, Body: []byte("{}")})
	if !res.Delivered || len(res.Attempts) != 3 || !res.Attempts[0].Retry || res.Attempts[2].Retry {
		t.Fatalf("%+v", res)
	}
}

func TestDeliverDoesNotRetryAPermanentRefusal(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&n, 1); w.WriteHeader(400) }))
	defer srv.Close()
	res := fast().Deliver(context.Background(), Request{URL: srv.URL})
	if res.Delivered || n != 1 || res.Last().Retry {
		t.Fatalf("%+v n=%d", res, n)
	}
}

func TestDeliverGivesUpAfterMaxAttemptsAndReportsEach(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	s := fast()
	s.MaxAttempts = 4
	res := s.Deliver(context.Background(), Request{URL: srv.URL})
	if res.Delivered || len(res.Attempts) != 4 || res.Last().Retry || res.Last().Status != 500 {
		t.Fatalf("%+v", res)
	}
}

func TestDeliverRetriesNetworkErrorsAndHonoursRetryAfter(t *testing.T) {
	// network error: nothing listening
	res := fast().Deliver(context.Background(), Request{URL: "http://127.0.0.1:1/x"})
	if res.Delivered || len(res.Attempts) != 5 || res.Attempts[0].Error == "" {
		t.Fatalf("%+v", res)
	}

	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	var waited time.Duration
	s := &Sender{Backoff: func(int) time.Duration { return time.Millisecond }, Sleep: func(_ context.Context, d time.Duration) { waited = d }}
	if r := s.Deliver(context.Background(), Request{URL: srv.URL}); !r.Delivered || waited != 7*time.Second {
		t.Fatalf("%+v waited=%v", r, waited)
	}
}

func TestDeliverStopsWhenTheContextIsCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := fast().Deliver(ctx, Request{URL: srv.URL})
	if res.Delivered || len(res.Attempts) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestSignAllCarriesEverySecretAndEachVerifies(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	h := SignAll([]string{"whsec_new", "whsec_old"}, t0, body)
	if strings.Count(h, "v1=") != 2 {
		t.Fatalf("want two v1 signatures, got %q", h)
	}
	for _, secret := range []string{"whsec_new", "whsec_old"} {
		if err := Verify(secret, h, body, t0.Add(time.Second), 0); err != nil {
			t.Fatalf("%s must verify during rotation: %v", secret, err)
		}
	}
	if err := Verify("whsec_other", h, body, t0, 0); err != ErrSignature {
		t.Fatalf("an unrelated secret must not verify, got %v", err)
	}
	if SignAll([]string{"whsec_a"}, t0, body) != Sign("whsec_a", t0, body) {
		t.Fatal("a single secret must equal Sign")
	}
}

func TestDeliverCapturesResponseBodyAndSignsWithAllSecrets(t *testing.T) {
	var header string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("X-Test-Sig")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, strings.Repeat("x", 5000))
	}))
	defer srv.Close()
	s := &Sender{Header: "X-Test-Sig", MaxAttempts: 1}
	res := s.Deliver(context.Background(), Request{URL: srv.URL, Secrets: []string{"a", "b"}, Body: []byte("{}")})
	if res.Delivered || len(res.Attempts) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := len(res.Last().ResponseBody); got != MaxResponseBody {
		t.Fatalf("response body must be truncated to %d bytes, got %d", MaxResponseBody, got)
	}
	if strings.Count(header, "v1=") != 2 {
		t.Fatalf("both secrets must sign the request, header=%q", header)
	}
}

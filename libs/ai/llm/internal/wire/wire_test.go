package wire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
)

func TestSSE(t *testing.T) {
	in := ": keep-alive\r\nevent: a\r\ndata: one\r\ndata: two\r\n\r\n\n\ndata: {\"x\":1}\n\nevent: last\ndata: tail"
	s := NewSSE(strings.NewReader(in))
	var got []Event
	for {
		e, ok := s.Next()
		if !ok {
			break
		}
		got = append(got, e)
	}
	if len(got) != 3 || got[0].Name != "a" || string(got[0].Data) != "one\ntwo" || string(got[1].Data) != `{"x":1}` || got[1].Name != "" ||
		got[2].Name != "last" || string(got[2].Data) != "tail" || s.Err() != nil {
		t.Fatalf("events = %+v err %v", got, s.Err())
	}
}

func client(url string, retries int) *Client {
	return &Client{Provider: "p", BaseURL: url, MaxRetries: retries, Auth: func(*http.Request, string) {},
		Key: KeyFrom("p", "k", nil, "")}
}

func TestPostDoesNotRetryABadRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(400) }))
	defer srv.Close()
	_, err := client(srv.URL, 3).Post(context.Background(), "/x", map[string]any{})
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Kind != core.KindInvalid || calls != 1 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

func TestPostRetriesThenGivesUp(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After-Ms", "5")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	_, err := client(srv.URL, 2).Post(context.Background(), "/x", map[string]any{})
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Kind != core.KindRateLimit || pe.RetryAfter != 5*time.Millisecond || calls != 3 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

func TestPostStopsWaitingWhenCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "20")
		w.WriteHeader(503)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := client(srv.URL, 2).Post(ctx, "/x", map[string]any{})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestNoKeyIsAnAuthError(t *testing.T) {
	_, err := KeyFrom("p", "", nil, "")(context.Background())
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Kind != core.KindAuth {
		t.Fatalf("err %v", err)
	}
}

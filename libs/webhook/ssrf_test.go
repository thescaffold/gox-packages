package webhook

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSafeClientRefusesLoopbackAndPrivateDestinations(t *testing.T) {
	// httptest listens on 127.0.0.1, which a safe client must refuse.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()

	s := &Sender{Client: NewSafeClient(2 * time.Second), MaxAttempts: 1}
	res := s.Deliver(context.Background(), Request{URL: srv.URL, Body: []byte("{}")})
	if res.Delivered {
		t.Fatal("delivery to a loopback address must be refused")
	}
	if res.Last().Error == "" || !contains(res.Last().Error, ErrBlockedAddress.Error()) {
		t.Fatalf("want a blocked-address error, got %q", res.Last().Error)
	}

	for _, url := range []string{"http://10.0.0.5/", "http://192.168.1.1/", "http://169.254.169.254/latest/meta-data/", "http://[::1]/"} {
		res := s.Deliver(context.Background(), Request{URL: url, Body: []byte("{}")})
		if res.Delivered || !contains(res.Last().Error, ErrBlockedAddress.Error()) {
			t.Fatalf("%s must be blocked, got %+v", url, res.Last())
		}
	}
}

func TestSafeClientDoesNotFollowRedirects(t *testing.T) {
	c := NewSafeClient(time.Second)
	if err := c.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirects must not be followed, got %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

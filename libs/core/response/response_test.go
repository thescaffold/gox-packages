package response

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func body(t *testing.T, o interface{ Data() any }) map[string]any {
	t.Helper()
	b, err := json.Marshal(o.Data())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestErrorsCarryCodeAndRetryable(t *testing.T) {
	cases := []struct {
		out   interface{ Data() any }
		code  string
		retry bool
	}{
		{NotFound("social", "not found"), "not_found", false},
		{Unauthorized("social", "sign in required"), "unauthorized", false},
		{BadRequest("social", "bad"), "bad_request", false},
		{Conflict("social", "taken"), "conflict", false},
		{InternalServerError("social", "boom"), "internal", true},
		{Error("social", "slow down", http.StatusTooManyRequests), "too_many_requests", true},
		{Error("social", "down", http.StatusServiceUnavailable), "service_unavailable", true},
		{ErrorCode("social", "that username is taken", http.StatusConflict, "username_taken"), "username_taken", false},
	}
	for _, c := range cases {
		m := body(t, c.out)
		if m["status"] != "error" || m["code"] != c.code || m["retryable"] != c.retry {
			t.Errorf("got %v, want code %q retryable %v", m, c.code, c.retry)
		}
		if _, has := m["message"]; !has {
			t.Errorf("message missing: %v", m)
		}
	}
}

func TestInvalidNamesFields(t *testing.T) {
	m := body(t, Invalid("social", "check the form", FieldError{Name: "username", Message: "too short"}))
	if m["code"] != "invalid" {
		t.Fatalf("code %v", m["code"])
	}
	f := m["fields"].([]any)
	if len(f) != 1 || f[0].(map[string]any)["name"] != "username" {
		t.Fatalf("fields %v", f)
	}
}

func TestSuccessIsUnchanged(t *testing.T) {
	m := body(t, Success(map[string]any{"a": 1}, "social", "ok", nil))
	for _, k := range []string{"code", "retryable", "fields"} {
		if _, has := m[k]; has {
			t.Errorf("success body has %q: %v", k, m)
		}
	}
}

// ErrorBody documents the error body; it must name exactly what Error writes.
func TestErrorBodyMatchesTheWire(t *testing.T) {
	wire := body(t, Invalid("social", "x", FieldError{Name: "n", Message: "m"}))
	delete(wire, "data") // always null on errors; not documented
	var got []string
	for k := range wire {
		got = append(got, k)
	}
	var want []string
	rt := reflect.TypeOf(ErrorBody{})
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		want = append(want, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wire %v, ErrorBody %v", got, want)
	}
}

func TestInternalLogsButDoesNotTell(t *testing.T) {
	var logged strings.Builder
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)
	m := body(t, Internal("social", errors.New(`pq: relation "SocialPosts" does not exist`)))
	if m["message"] != InternalMessage || m["code"] != "internal" || m["retryable"] != true {
		t.Fatalf("got %v", m)
	}
	if !strings.Contains(logged.String(), "SocialPosts") {
		t.Fatalf("the error must reach the server log, got %q", logged.String())
	}
}

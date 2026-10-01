package tests

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ntxhttp "github.com/thescaffold/gox-packages/libs/core/http"
)

// A body sent with a form content type must actually be a form: providers such
// as Mailgun reject a JSON body labelled multipart/form-data.
func formServer(t *testing.T, got *http.Request) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			_ = r.ParseMultipartForm(1 << 20)
		} else {
			_ = r.ParseForm()
		}
		*got = *r
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestExternal_MultipartFormBodyIsARealMultipartForm(t *testing.T) {
	var got http.Request
	srv := formServer(t, &got)
	ok, _, _, _, _ := ntxhttp.New("k").External("POST", srv.URL, map[string]any{"to": "a@b.test", "subject": "Hi <there>", "n": 3},
		nil, map[string]string{"content-type": "multipart/form-data"}, 0)
	if !ok {
		t.Fatal("request failed")
	}
	if ct := got.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data; boundary=") {
		t.Fatalf("content-type %q has no boundary", ct)
	}
	if got.FormValue("to") != "a@b.test" || got.FormValue("subject") != "Hi <there>" || got.FormValue("n") != "3" {
		t.Fatalf("form = %v", got.MultipartForm)
	}
}

func TestExternal_URLEncodedFormBodyIsARealForm(t *testing.T) {
	var got http.Request
	srv := formServer(t, &got)
	ok, _, _, _, _ := ntxhttp.New("k").External("POST", srv.URL, map[string]any{"to": "a@b.test", "tag": []string{"x", "y"}},
		nil, map[string]string{"content-type": "application/x-www-form-urlencoded"}, 0)
	if !ok {
		t.Fatal("request failed")
	}
	if got.FormValue("to") != "a@b.test" || strings.Join(got.Form["tag"], ",") != "x,y" {
		t.Fatalf("form = %v", got.Form)
	}
}

func TestExternal_JSONStaysTheDefault(t *testing.T) {
	var gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		b := make([]byte, 64)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	ntxhttp.New("k").External("POST", srv.URL, map[string]any{"a": 1}, nil, nil, 0)
	if gotCT != "application/json" || gotBody != `{"a":1}` {
		t.Fatalf("ct=%q body=%q", gotCT, gotBody)
	}
}

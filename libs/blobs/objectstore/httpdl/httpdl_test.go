package httpdl_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/blobs/objectstore"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/fs"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/httpdl"
)

var secret = []byte("httpdl-test-secret-0123456789abc")

func setup(t *testing.T) (objectstore.ObjectStore, http.Handler) {
	t.Helper()
	st, err := fs.New(t.TempDir(), fs.Config{PresignSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	return st, httpdl.New(httpdl.Config{Store: st, Secret: secret})
}

func get(h http.Handler, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func put(t *testing.T, st objectstore.ObjectStore, key, body, media string) {
	t.Helper()
	if _, err := st.Put(t.Context(), key, strings.NewReader(body), objectstore.PutOptions{MediaType: media}); err != nil {
		t.Fatal(err)
	}
}

func presign(t *testing.T, st objectstore.ObjectStore, key string, o objectstore.PresignOptions) string {
	t.Helper()
	u, err := st.Presign(t.Context(), key, o)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestServesTheObjectWithSafeHeaders(t *testing.T) {
	st, h := setup(t)
	put(t, st, "ws/a/r.pdf", "PDFDATA", "application/pdf")
	w := get(h, presign(t, st, "ws/a/r.pdf", objectstore.PresignOptions{Filename: "Q3 report.pdf"}), nil)
	if w.Code != 200 || w.Body.String() != "PDFDATA" {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	hd := w.Header()
	if hd.Get("Content-Type") != "application/pdf" || hd.Get("Content-Length") != "7" {
		t.Fatalf("headers %v", hd)
	}
	if cd := hd.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "Q3 report.pdf") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if hd.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(hd.Get("Cache-Control"), "private") || hd.Get("ETag") == "" || hd.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("missing safety headers: %v", hd)
	}
	if !strings.Contains(hd.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("a downloaded HTML/SVG must not run scripts in our origin: CSP=%q", hd.Get("Content-Security-Policy"))
	}
}

func TestFilenameCannotInjectHeaders(t *testing.T) {
	st, h := setup(t)
	put(t, st, "ws/a/x", "x", "text/plain")
	for _, name := range []string{"a\r\nSet-Cookie: pwned=1", "a\"; filename*=utf-8''evil", "../../etc/passwd", "ünï cödé.txt"} {
		w := get(h, presign(t, st, "ws/a/x", objectstore.PresignOptions{Filename: name}), nil)
		if w.Header().Get("Set-Cookie") != "" || strings.ContainsAny(w.Header().Get("Content-Disposition"), "\r\n") {
			t.Fatalf("filename %q injected a header: %v", name, w.Header())
		}
		if strings.Contains(w.Header().Get("Content-Disposition"), "../") {
			t.Fatalf("path traversal survived in %q", w.Header().Get("Content-Disposition"))
		}
	}
}

func TestRangeRequests(t *testing.T) {
	st, h := setup(t)
	put(t, st, "ws/a/v.bin", "0123456789", "video/mp4")
	u := presign(t, st, "ws/a/v.bin", objectstore.PresignOptions{})
	for _, c := range []struct{ rng, want, cr string }{
		{"bytes=0-3", "0123", "bytes 0-3/10"},
		{"bytes=7-", "789", "bytes 7-9/10"},
		{"bytes=-2", "89", "bytes 8-9/10"},
	} {
		w := get(h, u, map[string]string{"Range": c.rng})
		if w.Code != http.StatusPartialContent || w.Body.String() != c.want || w.Header().Get("Content-Range") != c.cr {
			t.Errorf("%s: %d %q %q", c.rng, w.Code, w.Body.String(), w.Header().Get("Content-Range"))
		}
	}
	w := get(h, u, map[string]string{"Range": "bytes=50-60"})
	if w.Code != http.StatusRequestedRangeNotSatisfiable || w.Header().Get("Content-Range") != "bytes */10" {
		t.Fatalf("unsatisfiable: %d %v", w.Code, w.Header())
	}
	if w := get(h, u, map[string]string{"Range": "bytes=abc"}); w.Code != 200 && w.Code != 416 {
		t.Fatalf("garbage range: %d", w.Code)
	}
}

func TestConditionalRequests(t *testing.T) {
	st, h := setup(t)
	put(t, st, "ws/a/c", "cached", "text/plain")
	u := presign(t, st, "ws/a/c", objectstore.PresignOptions{})
	etag := get(h, u, nil).Header().Get("ETag")
	if w := get(h, u, map[string]string{"If-None-Match": etag}); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("If-None-Match: %d", w.Code)
	}
	if w := get(h, u, map[string]string{"If-None-Match": `"other"`}); w.Code != 200 {
		t.Fatalf("a different ETag must serve the body: %d", w.Code)
	}
}

func TestRejectsBadTokens(t *testing.T) {
	st, h := setup(t)
	put(t, st, "ws/a/k", "k", "text/plain")
	good := presign(t, st, "ws/a/k", objectstore.PresignOptions{})
	forged, _ := objectstore.SignDownload("ws/a/k", objectstore.PresignOptions{}, []byte("an-attackers-own-secret-xxxxxxxx"), time.Now())
	expired, _ := objectstore.SignDownload("ws/a/k", objectstore.PresignOptions{}, secret, time.Now().Add(-time.Hour))
	cases := map[string]struct {
		path string
		want int
	}{
		"garbage":    {objectstore.DownloadPathPrefix + "nonsense", 403},
		"empty":      {objectstore.DownloadPathPrefix, 404},
		"forged":     {forged, 403},
		"tampered":   {good[:len(good)-3] + "AAA", 403},
		"expired":    {expired, 410},
		"wrong path": {"/api/blobs/other/" + strings.TrimPrefix(good, objectstore.DownloadPathPrefix), 404},
	}
	for name, c := range cases {
		if w := get(h, c.path, nil); w.Code != c.want {
			t.Errorf("%s: HTTP %d, want %d", name, w.Code, c.want)
		}
	}
	// A token for an object that has since been deleted is a 404, not a 500.
	_ = st.Delete(t.Context(), "ws/a/k")
	if w := get(h, good, nil); w.Code != http.StatusNotFound {
		t.Errorf("deleted object: %d", w.Code)
	}
	// Only GET and HEAD.
	req := httptest.NewRequest(http.MethodPost, good, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.Code)
	}
}

func TestSingleUseTokensWorkOnce(t *testing.T) {
	st, h := setup(t)
	put(t, st, "ws/a/once", "secret", "text/plain")
	u := presign(t, st, "ws/a/once", objectstore.PresignOptions{SingleUse: true})
	if w := get(h, u, nil); w.Code != 200 {
		t.Fatalf("first use: %d", w.Code)
	}
	if w := get(h, u, nil); w.Code != http.StatusGone {
		t.Fatalf("second use: %d, want 410", w.Code)
	}
	// Reusable tokens stay reusable.
	r := presign(t, st, "ws/a/once", objectstore.PresignOptions{})
	for i := 0; i < 3; i++ {
		if w := get(h, r, nil); w.Code != 200 {
			t.Fatalf("reusable token use %d: %d", i, w.Code)
		}
	}
}

func TestHEADHasNoBody(t *testing.T) {
	st, h := setup(t)
	put(t, st, "ws/a/h", "12345", "text/plain")
	req := httptest.NewRequest(http.MethodHead, presign(t, st, "ws/a/h", objectstore.PresignOptions{}), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "5" {
		t.Fatalf("%d %d %q", w.Code, w.Body.Len(), w.Header().Get("Content-Length"))
	}
}

func TestStreamsLargeBodiesWithoutBuffering(t *testing.T) {
	st, h := setup(t)
	big := strings.Repeat("0123456789abcdef", 1<<16) // 1 MiB
	put(t, st, "ws/a/big", big, "application/octet-stream")
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + presign(t, st, "ws/a/big", objectstore.PresignOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if len(b) != len(big) || string(b) != big {
		t.Fatalf("got %d bytes", len(b))
	}
}

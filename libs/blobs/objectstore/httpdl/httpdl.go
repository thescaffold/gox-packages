// Package httpdl is the download route behind objectstore.Presign for drivers
// without native presigned URLs (TRD §6.10): GET/HEAD /api/blobs/dl/<token>.
// The token is an HMAC-signed claim set (key, expiry, filename, single-use);
// the handler verifies it, streams the object from the store with Range and
// conditional-request support, and sets the headers that make serving
// user-produced bytes safe (attachment disposition, nosniff, a sandboxing
// CSP, private caching). Nothing is ever publicly readable: no valid token,
// no bytes.
package httpdl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thescaffold/gox-packages/libs/blobs/objectstore"
)

// Spent records single-use tokens as used. MarkSpent reports true exactly once
// per id. The in-memory default is per process; run more than one instance
// with a shared implementation (the Postgres driver provides one).
type Spent interface {
	MarkSpent(ctx context.Context, id string, expires time.Time) (bool, error)
}

// Config configures the handler.
type Config struct {
	Store  objectstore.ObjectStore
	Secret []byte
	Spent  Spent
	Now    func() time.Time
}

type handler struct{ cfg Config }

// New returns the download handler; mount it at objectstore.DownloadPathPrefix.
func New(cfg Config) http.Handler {
	if cfg.Spent == nil {
		cfg.Spent = &memSpent{used: map[string]time.Time{}}
	}
	return &handler{cfg: cfg}
}

type memSpent struct {
	mu   sync.Mutex
	used map[string]time.Time
}

func (m *memSpent) MarkSpent(_ context.Context, id string, expires time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for k, e := range m.used { // forget tokens that have expired anyway
		if now.After(e) {
			delete(m.used, k)
		}
	}
	if _, seen := m.used[id]; seen {
		return false, nil
	}
	m.used[id] = expires
	return true, nil
}

func (h *handler) now() time.Time {
	if h.cfg.Now != nil {
		return h.cfg.Now()
	}
	return time.Now()
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, msg, code)
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tok, ok := strings.CutPrefix(r.URL.Path, objectstore.DownloadPathPrefix)
	if !ok || tok == "" {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	claims, err := objectstore.VerifyDownload(tok, h.cfg.Secret, h.now())
	switch {
	case errors.Is(err, objectstore.ErrTokenExpired):
		fail(w, http.StatusGone, "this download link has expired")
		return
	case err != nil:
		fail(w, http.StatusForbidden, "invalid download link")
		return
	}
	ctx := r.Context()
	info, err := h.cfg.Store.Head(ctx, claims.Key)
	if errors.Is(err, objectstore.ErrNotFound) {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "storage error")
		return
	}

	etag := `"` + info.SHA256 + `"`
	hd := w.Header()
	hd.Set("ETag", etag)
	hd.Set("Accept-Ranges", "bytes")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	hd.Set("Cache-Control", "private, max-age=0, must-revalidate")
	hd.Set("Content-Disposition", disposition(claims.Filename, claims.Key))
	ct := info.MediaType
	if ct == "" {
		ct = "application/octet-stream"
	}
	hd.Set("Content-Type", ct)

	if matchETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	var rng *objectstore.Range
	if rh := r.Header.Get("Range"); rh != "" {
		rng = parseRange(rh)
	}
	start, length, err := objectstore.ResolveRange(rng, info.Size)
	if err != nil {
		hd.Set("Content-Range", fmt.Sprintf("bytes */%d", info.Size))
		fail(w, http.StatusRequestedRangeNotSatisfiable, "range not satisfiable")
		return
	}

	if claims.SingleUse && r.Method == http.MethodGet {
		first, err := h.cfg.Spent.MarkSpent(ctx, claims.ID, claims.Expires)
		if err != nil {
			fail(w, http.StatusInternalServerError, "storage error")
			return
		}
		if !first {
			fail(w, http.StatusGone, "this download link has already been used")
			return
		}
	}

	hd.Set("Content-Length", strconv.FormatInt(length, 10))
	status := http.StatusOK
	if rng != nil {
		status = http.StatusPartialContent
		hd.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, info.Size))
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}
	rc, _, err := h.cfg.Store.Get(ctx, claims.Key, rng)
	if err != nil {
		fail(w, http.StatusInternalServerError, "storage error")
		return
	}
	defer func() { _ = rc.Close() }()
	w.WriteHeader(status)
	_, _ = io.Copy(w, rc) // a client that goes away ends the copy; nothing else to do
}

func matchETag(header, etag string) bool {
	for _, p := range strings.Split(header, ",") {
		p = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "W/"))
		if p == etag || p == "*" {
			return true
		}
	}
	return false
}

// parseRange understands the single-range forms bytes=a-b, a- and -n. Anything
// else (malformed, multiple ranges) yields nil: serve the whole object, as
// RFC 9110 permits.
func parseRange(h string) *objectstore.Range {
	spec, ok := strings.CutPrefix(strings.TrimSpace(h), "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return nil
	}
	a, b, ok := strings.Cut(spec, "-")
	if !ok {
		return nil
	}
	if a == "" {
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return nil
		}
		return &objectstore.Range{Suffix: n}
	}
	start, err := strconv.ParseInt(a, 10, 64)
	if err != nil || start < 0 {
		return nil
	}
	if b == "" {
		return &objectstore.Range{Start: start, OpenEnd: true}
	}
	end, err := strconv.ParseInt(b, 10, 64)
	if err != nil {
		return nil
	}
	return &objectstore.Range{Start: start, End: end}
}

// disposition builds a safe Content-Disposition: an ASCII-only quoted
// filename plus an RFC 5987 UTF-8 form, with path separators, quotes and
// control characters removed so a filename cannot inject a header or suggest a
// path.
func disposition(name, key string) string {
	if name == "" {
		name = key[strings.LastIndex(key, "/")+1:]
	}
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var clean strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' || r == '/' {
			continue
		}
		clean.WriteRune(r)
	}
	name = strings.TrimSpace(strings.TrimLeft(clean.String(), "."))
	if name == "" {
		name = "download"
	}
	var ascii strings.Builder
	for _, r := range name {
		if r < 0x80 {
			ascii.WriteRune(r)
		} else {
			ascii.WriteByte('_')
		}
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii.String(), url.PathEscape(name))
}

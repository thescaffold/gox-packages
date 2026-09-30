// Package storetest is the driver-agnostic contract suite for
// objectstore.ObjectStore (PLAN M1-05). Every driver — fs, postgres, s3 — runs
// exactly these tests, so nothing driver-specific can leak into a caller.
//
//	func TestMyDriver(t *testing.T) {
//	    storetest.Run(t, storetest.Config{
//	        New: func(t *testing.T) objectstore.ObjectStore { return newMyDriver(t) },
//	        PresignSecret: secret, MaxObjectSize: 1 << 20,
//	    })
//	}
package storetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/blobs/objectstore"
)

// Config tells the suite how to build a fresh, empty store.
type Config struct {
	// New returns an empty store; the suite calls it once per test.
	New func(t *testing.T) objectstore.ObjectStore
	// PresignSecret is the HMAC secret the store signs download URLs with.
	PresignSecret []byte
	// MaxObjectSize is the per-object cap New configures (the suite checks
	// that exceeding it fails with ErrTooLarge).
	MaxObjectSize int64
	// LargeBytes is the size of the streaming test; 0 means 24 MiB.
	LargeBytes int64
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func mustPut(t *testing.T, s objectstore.ObjectStore, key string, data []byte, o objectstore.PutOptions) objectstore.ObjectInfo {
	t.Helper()
	info, err := s.Put(context.Background(), key, bytes.NewReader(data), o)
	if err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
	return info
}

func readAll(t *testing.T, s objectstore.ObjectStore, key string, rng *objectstore.Range) ([]byte, objectstore.ObjectInfo) {
	t.Helper()
	rc, info, err := s.Get(context.Background(), key, rng)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	return b, info
}

// Run executes the whole suite.
func Run(t *testing.T, cfg Config) {
	t.Helper()
	cases := []struct {
		name string
		fn   func(*testing.T, Config)
	}{
		{"PutGetRoundTripAndChecksum", putGet},
		{"EmptyObject", emptyObject},
		{"HeadAndMetadata", headMeta},
		{"NotFound", notFound},
		{"OverwriteReplacesAtomically", overwrite},
		{"IfNotExists", ifNotExists},
		{"ExpectedChecksum", expectedChecksum},
		{"FailedPutLeavesNothing", failedPut},
		{"Ranges", ranges},
		{"DeleteIsIdempotent", deleteIdem},
		{"ListPrefixOrderAndPagination", list},
		{"Copy", copyObj},
		{"KeyValidation", keyValidation},
		{"SizeLimit", sizeLimit},
		{"Multipart", multipart},
		{"MultipartAbortLeavesNothing", multipartAbort},
		{"MultipartRejectsBadParts", multipartBad},
		{"MultipartIDsAreUnguessable", multipartIDs},
		{"ConcurrentPutsNeverCorrupt", concurrent},
		{"LargeObjectStreamsWithBoundedMemory", large},
		{"Presign", presign},
		{"WorkspaceIsolation", workspaceIsolation},
		{"ContextCancellation", ctxCancel},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { c.fn(t, cfg) })
	}
}

func putGet(t *testing.T, cfg Config) {
	s := cfg.New(t)
	data := randBytes(t, 300_000)
	info := mustPut(t, s, "ws/a/obj/one.bin", data, objectstore.PutOptions{MediaType: "application/octet-stream"})
	if info.Size != int64(len(data)) || info.SHA256 != sum(data) || info.Key != "ws/a/obj/one.bin" {
		t.Fatalf("Put info = %+v", info)
	}
	got, ginfo := readAll(t, s, "ws/a/obj/one.bin", nil)
	if !bytes.Equal(got, data) {
		t.Fatal("bytes differ after a round trip")
	}
	if ginfo.Size != info.Size || ginfo.SHA256 != info.SHA256 {
		t.Fatalf("Get info %+v != Put info %+v", ginfo, info)
	}
}

func emptyObject(t *testing.T, cfg Config) {
	s := cfg.New(t)
	info := mustPut(t, s, "ws/a/empty", nil, objectstore.PutOptions{})
	if info.Size != 0 || info.SHA256 != sum(nil) {
		t.Fatalf("empty object info = %+v", info)
	}
	if got, _ := readAll(t, s, "ws/a/empty", nil); len(got) != 0 {
		t.Fatal("empty object returned bytes")
	}
}

func headMeta(t *testing.T, cfg Config) {
	s := cfg.New(t)
	before := time.Now().Add(-2 * time.Second)
	mustPut(t, s, "ws/a/m", []byte("hello"), objectstore.PutOptions{
		MediaType: "text/plain", Metadata: map[string]string{"Producer": "builder", "run": "r1"},
	})
	h, err := s.Head(context.Background(), "ws/a/m")
	if err != nil {
		t.Fatal(err)
	}
	if h.Size != 5 || h.SHA256 != sum([]byte("hello")) || h.MediaType != "text/plain" {
		t.Fatalf("Head = %+v", h)
	}
	if h.Metadata["producer"] != "builder" || h.Metadata["run"] != "r1" {
		t.Fatalf("metadata not preserved (keys are lower-cased): %+v", h.Metadata)
	}
	if h.CreatedAt.Before(before) || h.CreatedAt.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("CreatedAt = %v", h.CreatedAt)
	}
}

func notFound(t *testing.T, cfg Config) {
	s := cfg.New(t)
	ctx := context.Background()
	if _, err := s.Head(ctx, "ws/a/missing"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("Head: %v", err)
	}
	if _, _, err := s.Get(ctx, "ws/a/missing", nil); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("Get: %v", err)
	}
	if _, err := s.Copy(ctx, "ws/a/missing", "ws/a/dst"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("Copy: %v", err)
	}
	if _, err := s.Presign(ctx, "ws/a/missing", objectstore.PresignOptions{}); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("Presign of a missing key must not hand out a URL: %v", err)
	}
}

func overwrite(t *testing.T, cfg Config) {
	s := cfg.New(t)
	mustPut(t, s, "ws/a/k", []byte("old"), objectstore.PutOptions{})
	mustPut(t, s, "ws/a/k", []byte("the new content"), objectstore.PutOptions{})
	got, info := readAll(t, s, "ws/a/k", nil)
	if string(got) != "the new content" || info.Size != 15 || info.SHA256 != sum([]byte("the new content")) {
		t.Fatalf("after overwrite: %q %+v", got, info)
	}
	// A reader that opened the old object before the overwrite keeps reading the OLD bytes intact.
	big := randBytes(t, 1<<20)
	mustPut(t, s, "ws/a/held", big, objectstore.PutOptions{})
	rc, _, err := s.Get(context.Background(), "ws/a/held", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	first := make([]byte, 1000)
	if _, err := io.ReadFull(rc, first); err != nil {
		t.Fatal(err)
	}
	mustPut(t, s, "ws/a/held", randBytes(t, 2<<20), objectstore.PutOptions{})
	rest, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("an in-flight read was broken by an overwrite: %v", err)
	}
	if !bytes.Equal(append(first, rest...), big) {
		t.Fatal("an in-flight read saw a torn mix of old and new bytes")
	}
}

func ifNotExists(t *testing.T, cfg Config) {
	s := cfg.New(t)
	mustPut(t, s, "ws/a/once", []byte("first"), objectstore.PutOptions{IfNotExists: true})
	_, err := s.Put(context.Background(), "ws/a/once", strings.NewReader("second"), objectstore.PutOptions{IfNotExists: true})
	if !errors.Is(err, objectstore.ErrExists) {
		t.Fatalf("got %v, want ErrExists", err)
	}
	if got, _ := readAll(t, s, "ws/a/once", nil); string(got) != "first" {
		t.Fatalf("IfNotExists overwrote: %q", got)
	}
}

func expectedChecksum(t *testing.T, cfg Config) {
	s := cfg.New(t)
	data := []byte("checksummed")
	mustPut(t, s, "ws/a/good", data, objectstore.PutOptions{ExpectedSHA256: sum(data)})
	_, err := s.Put(context.Background(), "ws/a/bad", bytes.NewReader(data), objectstore.PutOptions{ExpectedSHA256: sum([]byte("other"))})
	if !errors.Is(err, objectstore.ErrChecksumMismatch) {
		t.Fatalf("got %v, want ErrChecksumMismatch", err)
	}
	if _, err := s.Head(context.Background(), "ws/a/bad"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("a rejected write left an object behind")
	}
}

type failingReader struct {
	data []byte
	at   int
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.at >= len(f.data) {
		return 0, errors.New("connection reset by peer")
	}
	n := copy(p, f.data[f.at:])
	f.at += n
	return n, nil
}

func failedPut(t *testing.T, cfg Config) {
	s := cfg.New(t)
	// A killed upload leaves no visible object...
	_, err := s.Put(context.Background(), "ws/a/half", &failingReader{data: randBytes(t, 500_000)}, objectstore.PutOptions{})
	if err == nil {
		t.Fatal("a failing reader must fail the Put")
	}
	if _, err := s.Head(context.Background(), "ws/a/half"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("a failed Put left a visible object: %v", err)
	}
	// ...and must not clobber an existing one either.
	mustPut(t, s, "ws/a/keep", []byte("intact"), objectstore.PutOptions{})
	_, _ = s.Put(context.Background(), "ws/a/keep", &failingReader{data: randBytes(t, 500_000)}, objectstore.PutOptions{})
	if got, _ := readAll(t, s, "ws/a/keep", nil); string(got) != "intact" {
		t.Fatalf("a failed overwrite damaged the existing object: %q", got)
	}
	if page, _ := s.List(context.Background(), "ws/a/", objectstore.ListOptions{}); len(page.Objects) != 1 {
		t.Fatalf("listing shows %d objects, want only the intact one", len(page.Objects))
	}
}

func ranges(t *testing.T, cfg Config) {
	s := cfg.New(t)
	data := randBytes(t, 10_000)
	mustPut(t, s, "ws/a/r", data, objectstore.PutOptions{})
	cases := []struct {
		name string
		r    objectstore.Range
		want []byte
	}{
		{"first 10", objectstore.Range{Start: 0, End: 9}, data[:10]},
		{"middle", objectstore.Range{Start: 1234, End: 5678}, data[1234:5679]},
		{"last byte", objectstore.Range{Start: 9999, End: 9999}, data[9999:]},
		{"open end", objectstore.Range{Start: 9000, OpenEnd: true}, data[9000:]},
		{"suffix", objectstore.Range{Suffix: 100}, data[9900:]},
		{"suffix larger than object", objectstore.Range{Suffix: 99_999}, data},
		{"end past EOF is clamped", objectstore.Range{Start: 9990, End: 50_000}, data[9990:]},
	}
	for _, c := range cases {
		r := c.r
		got, info := readAll(t, s, "ws/a/r", &r)
		if !bytes.Equal(got, c.want) {
			t.Errorf("%s: wrong bytes (got %d, want %d)", c.name, len(got), len(c.want))
		}
		if info.Size != 10_000 {
			t.Errorf("%s: info.Size = %d, want the whole object's 10000", c.name, info.Size)
		}
	}
	for name, r := range map[string]objectstore.Range{
		"start past EOF":   {Start: 10_000, End: 10_005},
		"end before start": {Start: 50, End: 10},
		"negative start":   {Start: -1, End: 5},
	} {
		r := r
		if _, _, err := s.Get(context.Background(), "ws/a/r", &r); !errors.Is(err, objectstore.ErrRangeNotSatisfiable) {
			t.Errorf("%s: got %v, want ErrRangeNotSatisfiable", name, err)
		}
	}
}

func deleteIdem(t *testing.T, cfg Config) {
	s := cfg.New(t)
	ctx := context.Background()
	mustPut(t, s, "ws/a/d", []byte("x"), objectstore.PutOptions{})
	if err := s.Delete(ctx, "ws/a/d"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Head(ctx, "ws/a/d"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("object survived Delete")
	}
	if err := s.Delete(ctx, "ws/a/d"); err != nil {
		t.Fatalf("deleting a missing key must not error: %v", err)
	}
}

func list(t *testing.T, cfg Config) {
	s := cfg.New(t)
	ctx := context.Background()
	keys := []string{"ws/a/x/1", "ws/a/x/2", "ws/a/x/10", "ws/a/y/1", "ws/b/x/1", "ws/a/x/sub/deep", "ws/ab/x", "zz/ws/a/contains-the-prefix-mid-key"}
	for _, k := range keys {
		mustPut(t, s, k, []byte(k), objectstore.PutOptions{})
	}
	all, err := s.List(ctx, "ws/a/", objectstore.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range all.Objects {
		got = append(got, o.Key)
	}
	want := []string{"ws/a/x/1", "ws/a/x/10", "ws/a/x/2", "ws/a/x/sub/deep", "ws/a/y/1"}
	if !sort.StringsAreSorted(got) || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("List(ws/a/) = %v, want %v (lexicographic, and 'ws/ab/' is NOT under 'ws/a/')", got, want)
	}
	// Pagination: walk it two at a time; no gaps, no repeats.
	var paged []string
	after := ""
	for i := 0; i < 10; i++ {
		page, err := s.List(ctx, "ws/a/", objectstore.ListOptions{After: after, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Objects) > 2 {
			t.Fatalf("page of %d with Limit 2", len(page.Objects))
		}
		for _, o := range page.Objects {
			paged = append(paged, o.Key)
		}
		if page.Next == "" {
			break
		}
		after = page.Next
	}
	if strings.Join(paged, ",") != strings.Join(want, ",") {
		t.Fatalf("paged listing = %v, want %v", paged, want)
	}
	// An empty prefix match is an empty page, not an error.
	if page, err := s.List(ctx, "ws/none/", objectstore.ListOptions{}); err != nil || len(page.Objects) != 0 || page.Next != "" {
		t.Fatalf("empty listing: %+v %v", page, err)
	}
}

func copyObj(t *testing.T, cfg Config) {
	s := cfg.New(t)
	data := randBytes(t, 200_000)
	src := mustPut(t, s, "ws/a/src", data, objectstore.PutOptions{MediaType: "image/png", Metadata: map[string]string{"k": "v"}})
	dst, err := s.Copy(context.Background(), "ws/a/src", "ws/a/dst")
	if err != nil {
		t.Fatal(err)
	}
	if dst.Key != "ws/a/dst" || dst.SHA256 != src.SHA256 || dst.Size != src.Size || dst.MediaType != "image/png" || dst.Metadata["k"] != "v" {
		t.Fatalf("copy info = %+v", dst)
	}
	// Independent: deleting the source leaves the copy whole.
	if err := s.Delete(context.Background(), "ws/a/src"); err != nil {
		t.Fatal(err)
	}
	if got, _ := readAll(t, s, "ws/a/dst", nil); !bytes.Equal(got, data) {
		t.Fatal("the copy depends on its source")
	}
}

func keyValidation(t *testing.T, cfg Config) {
	s := cfg.New(t)
	ctx := context.Background()
	bad := []string{"", "/abs", "trailing/", "a//b", "a/./b", "a/../b", "../escape", "a/..", "a\\b", "nul\x00byte", "ctl\x01", strings.Repeat("k", 1025), "bad\xffutf8"}
	for _, k := range bad {
		if _, err := s.Put(ctx, k, strings.NewReader("x"), objectstore.PutOptions{}); !errors.Is(err, objectstore.ErrInvalidKey) {
			t.Errorf("Put(%q): got %v, want ErrInvalidKey", k, err)
		}
		if _, _, err := s.Get(ctx, k, nil); !errors.Is(err, objectstore.ErrInvalidKey) {
			t.Errorf("Get(%q): got %v, want ErrInvalidKey", k, err)
		}
		if err := s.Delete(ctx, k); !errors.Is(err, objectstore.ErrInvalidKey) {
			t.Errorf("Delete(%q): got %v, want ErrInvalidKey", k, err)
		}
	}
	// Spaces, unicode and dots inside a segment are fine and must round-trip distinctly.
	for _, k := range []string{"ws/a/with space.txt", "ws/a/ünïcode/文件.md", "ws/a/.hidden", "ws/a/dots..inside", "ws/a/UPPER", "ws/a/upper"} {
		mustPut(t, s, k, []byte(k), objectstore.PutOptions{})
	}
	for _, k := range []string{"ws/a/UPPER", "ws/a/upper"} {
		if got, _ := readAll(t, s, k, nil); string(got) != k {
			t.Errorf("key %q returned %q (keys must be case-sensitive and distinct)", k, got)
		}
	}
}

func sizeLimit(t *testing.T, cfg Config) {
	if cfg.MaxObjectSize <= 0 {
		t.Skip("no size limit configured")
	}
	s := cfg.New(t)
	mustPut(t, s, "ws/a/atlimit", make([]byte, cfg.MaxObjectSize), objectstore.PutOptions{})
	_, err := s.Put(context.Background(), "ws/a/over", bytes.NewReader(make([]byte, cfg.MaxObjectSize+1)), objectstore.PutOptions{})
	if !errors.Is(err, objectstore.ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
	if !strings.Contains(err.Error(), "S3") {
		t.Fatalf("the message must name the S3 switch: %v", err)
	}
	if _, err := s.Head(context.Background(), "ws/a/over"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("an over-limit write left an object behind")
	}
}

func multipart(t *testing.T, cfg Config) {
	s := cfg.New(t)
	ctx := context.Background()
	p1, p2, p3 := randBytes(t, 100_000), randBytes(t, 70_000), randBytes(t, 5_000)
	id, err := s.BeginMultipart(ctx, "ws/a/mp", objectstore.PutOptions{MediaType: "video/mp4"})
	if err != nil {
		t.Fatal(err)
	}
	// The object is invisible until completion; parts arrive out of order; one is retried.
	var parts [3]objectstore.PartInfo
	up := func(n int, b []byte) objectstore.PartInfo {
		pi, err := s.UploadPart(ctx, id, n, bytes.NewReader(b))
		if err != nil {
			t.Fatalf("UploadPart %d: %v", n, err)
		}
		if pi.Number != n || pi.Size != int64(len(b)) || pi.SHA256 != sum(b) {
			t.Fatalf("part info %+v", pi)
		}
		return pi
	}
	parts[2] = up(3, p3)
	parts[0] = up(1, []byte("a failed first attempt"))
	parts[0] = up(1, p1) // retry replaces it
	if _, err := s.Head(ctx, "ws/a/mp"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("a multipart object is visible before CompleteMultipart")
	}
	parts[1] = up(2, p2)
	info, err := s.CompleteMultipart(ctx, id, parts[:])
	if err != nil {
		t.Fatal(err)
	}
	all := append(append(append([]byte{}, p1...), p2...), p3...)
	if info.Size != int64(len(all)) || info.SHA256 != sum(all) || info.MediaType != "video/mp4" {
		t.Fatalf("completed info = %+v", info)
	}
	if got, _ := readAll(t, s, "ws/a/mp", nil); !bytes.Equal(got, all) {
		t.Fatal("assembled bytes differ (parts must concatenate in NUMBER order, not arrival order)")
	}
	// The upload is finished: completing again fails.
	if _, err := s.CompleteMultipart(ctx, id, parts[:]); !errors.Is(err, objectstore.ErrUploadNotFound) {
		t.Fatalf("second complete: %v", err)
	}
}

func multipartAbort(t *testing.T, cfg Config) {
	s := cfg.New(t)
	ctx := context.Background()
	id, _ := s.BeginMultipart(ctx, "ws/a/ab", objectstore.PutOptions{})
	if _, err := s.UploadPart(ctx, id, 1, strings.NewReader("part")); err != nil {
		t.Fatal(err)
	}
	if err := s.AbortMultipart(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Head(ctx, "ws/a/ab"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("an aborted upload left an object")
	}
	if _, err := s.UploadPart(ctx, id, 2, strings.NewReader("x")); !errors.Is(err, objectstore.ErrUploadNotFound) {
		t.Fatalf("UploadPart after abort: %v", err)
	}
	if err := s.AbortMultipart(ctx, id); err != nil {
		t.Fatalf("abort must be idempotent: %v", err)
	}
	for _, bogus := range []string{"no-such-upload", "../../../../../../../../tmp/x", strings.Repeat("a", 32), strings.Repeat("../", 11) + "ab", ""} {
		if _, err := s.UploadPart(ctx, objectstore.UploadID(bogus), 1, strings.NewReader("x")); !errors.Is(err, objectstore.ErrUploadNotFound) {
			t.Fatalf("upload id %q: %v", bogus, err)
		}
		if err := s.AbortMultipart(ctx, objectstore.UploadID(bogus)); err != nil {
			t.Fatalf("abort of unknown id %q must be a no-op: %v", bogus, err)
		}
	}
}

func multipartBad(t *testing.T, cfg Config) {
	s := cfg.New(t)
	ctx := context.Background()
	id, _ := s.BeginMultipart(ctx, "ws/a/bad", objectstore.PutOptions{})
	good, err := s.UploadPart(ctx, id, 1, strings.NewReader("one"))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, -1, 10_001} {
		if _, err := s.UploadPart(ctx, id, n, strings.NewReader("x")); !errors.Is(err, objectstore.ErrInvalidPart) {
			t.Errorf("part number %d: got %v, want ErrInvalidPart", n, err)
		}
	}
	for name, parts := range map[string][]objectstore.PartInfo{
		"none":                  {},
		"a part never uploaded": {good, {Number: 2, Size: 1, SHA256: sum([]byte("x"))}},
		"wrong checksum":        {{Number: 1, Size: 3, SHA256: sum([]byte("not one"))}},
		"duplicate number":      {good, good},
		"out of order list":     {{Number: 2, Size: 1, SHA256: "x"}, good},
	} {
		if _, err := s.CompleteMultipart(ctx, id, parts); !errors.Is(err, objectstore.ErrInvalidPart) {
			t.Errorf("%s: got %v, want ErrInvalidPart", name, err)
		}
	}
	if _, err := s.Head(ctx, "ws/a/bad"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("a rejected completion created an object")
	}
	// A rejected completion must not destroy the upload: the right one still works.
	if _, err := s.CompleteMultipart(ctx, id, []objectstore.PartInfo{good}); err != nil {
		t.Fatalf("the valid completion failed after rejected ones: %v", err)
	}
}

func multipartIDs(t *testing.T, cfg Config) {
	s := cfg.New(t)
	seen := map[objectstore.UploadID]bool{}
	for i := 0; i < 50; i++ {
		id, err := s.BeginMultipart(context.Background(), fmt.Sprintf("ws/a/u%d", i), objectstore.PutOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(id) < 32 || seen[id] {
			t.Fatalf("upload id %q is short or repeated — it is a capability and must be unguessable", id)
		}
		seen[id] = true
		_ = s.AbortMultipart(context.Background(), id)
	}
}

func concurrent(t *testing.T, cfg Config) {
	s := cfg.New(t)
	const writers = 12
	contents := make([][]byte, writers)
	for i := range contents {
		contents[i] = randBytes(t, 200_000+i)
	}
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Put(context.Background(), "ws/a/race", bytes.NewReader(contents[i]), objectstore.PutOptions{}); err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	got, info := readAll(t, s, "ws/a/race", nil)
	for _, c := range contents {
		if bytes.Equal(got, c) {
			if info.SHA256 != sum(c) || info.Size != int64(len(c)) {
				t.Fatalf("metadata belongs to a different writer than the bytes: %+v", info)
			}
			return
		}
	}
	t.Fatal("the stored object is a mix of concurrent writers' bytes")
}

type countingReader struct {
	n, total int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.n >= c.total {
		return 0, io.EOF
	}
	k := int64(len(p))
	if c.total-c.n < k {
		k = c.total - c.n
	}
	for i := int64(0); i < k; i++ {
		p[i] = byte((c.n + i) * 31)
	}
	c.n += k
	return int(k), nil
}

func large(t *testing.T, cfg Config) {
	total := cfg.LargeBytes
	if total == 0 {
		total = 24 << 20
	}
	if cfg.MaxObjectSize > 0 && total > cfg.MaxObjectSize {
		total = cfg.MaxObjectSize
	}
	s := cfg.New(t)
	ctx := context.Background()

	var base runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&base)

	info, err := s.Put(ctx, "ws/a/large", &countingReader{total: total}, objectstore.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != total {
		t.Fatalf("size %d, want %d", info.Size, total)
	}
	rc, _, err := s.Get(ctx, "ws/a/large", nil)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var n int64
	var peak uint64
	for {
		k, err := rc.Read(buf)
		h.Write(buf[:k])
		n += int64(k)
		if n%(4<<20) < int64(k) {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak {
				peak = m.HeapAlloc
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	_ = rc.Close()
	if n != total || hex.EncodeToString(h.Sum(nil)) != info.SHA256 {
		t.Fatalf("large object read back %d bytes / wrong hash", n)
	}
	// Streamed, not buffered: the heap must stay far below the object's size.
	if growth := int64(peak) - int64(base.HeapAlloc); peak > base.HeapAlloc && growth > total/2 {
		t.Fatalf("heap grew by %d MiB while streaming a %d MiB object — the driver is buffering it", growth>>20, total>>20)
	}
}

func presign(t *testing.T, cfg Config) {
	s := cfg.New(t)
	ctx := context.Background()
	mustPut(t, s, "ws/a/dl.pdf", []byte("pdf"), objectstore.PutOptions{})
	u, err := s.Presign(ctx, "ws/a/dl.pdf", objectstore.PresignOptions{Filename: "Report.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, objectstore.DownloadPathPrefix) {
		t.Fatalf("url = %q", u)
	}
	c, err := objectstore.VerifyDownload(u, cfg.PresignSecret, time.Now())
	if err != nil {
		t.Fatalf("a freshly presigned URL does not verify: %v", err)
	}
	if c.Key != "ws/a/dl.pdf" || c.Filename != "Report.pdf" {
		t.Fatalf("claims = %+v", c)
	}
	ttl := time.Until(c.Expires)
	if ttl < 4*time.Minute || ttl > 16*time.Minute {
		t.Fatalf("default expiry %v, want 5-15 minutes", ttl)
	}
	if _, err := objectstore.VerifyDownload(u, cfg.PresignSecret, time.Now().Add(time.Hour)); !errors.Is(err, objectstore.ErrTokenExpired) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := objectstore.VerifyDownload(u, []byte("a-different-secret-entirely"), time.Now()); !errors.Is(err, objectstore.ErrBadToken) {
		t.Fatalf("wrong secret: %v", err)
	}
	if _, err := objectstore.VerifyDownload(u[:len(u)-3]+"abc", cfg.PresignSecret, time.Now()); !errors.Is(err, objectstore.ErrBadToken) {
		t.Fatalf("tampered: %v", err)
	}
	// A requested TTL is clamped into 5-15 minutes.
	long, _ := s.Presign(ctx, "ws/a/dl.pdf", objectstore.PresignOptions{TTL: 48 * time.Hour})
	lc, _ := objectstore.VerifyDownload(long, cfg.PresignSecret, time.Now())
	if time.Until(lc.Expires) > 16*time.Minute {
		t.Fatalf("a 48h TTL was honoured: %v", time.Until(lc.Expires))
	}
	u2, _ := s.Presign(ctx, "ws/a/dl.pdf", objectstore.PresignOptions{SingleUse: true})
	c2, _ := objectstore.VerifyDownload(u2, cfg.PresignSecret, time.Now())
	if !c2.SingleUse || c2.ID == c.ID {
		t.Fatal("single-use flag / unique token id missing")
	}
}

func workspaceIsolation(t *testing.T, cfg Config) {
	base := cfg.New(t)
	ctx := context.Background()
	a, err := objectstore.ForWorkspace(base, "wsA")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := objectstore.ForWorkspace(base, "wsB")
	mustPut(t, a, "ws/wsA/secret", []byte("A's bytes"), objectstore.PutOptions{})
	mustPut(t, b, "ws/wsB/own", []byte("B's bytes"), objectstore.PutOptions{})

	forbidden := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, objectstore.ErrForbidden) {
			t.Errorf("%s: got %v, want ErrForbidden", name, err)
		}
	}
	_, _, err = b.Get(ctx, "ws/wsA/secret", nil)
	forbidden("B reads A's key", err)
	_, err = b.Put(ctx, "ws/wsA/secret", strings.NewReader("overwritten"), objectstore.PutOptions{})
	forbidden("B overwrites A's key", err)
	forbidden("B deletes A's key", b.Delete(ctx, "ws/wsA/secret"))
	_, err = b.Head(ctx, "ws/wsA/secret")
	forbidden("B heads A's key", err)
	_, err = b.Copy(ctx, "ws/wsA/secret", "ws/wsB/stolen")
	forbidden("B copies out of A", err)
	_, err = b.Copy(ctx, "ws/wsB/own", "ws/wsA/planted")
	forbidden("B copies into A", err)
	_, err = b.Presign(ctx, "ws/wsA/secret", objectstore.PresignOptions{})
	forbidden("B presigns A's key", err)
	_, err = b.BeginMultipart(ctx, "ws/wsA/mp", objectstore.PutOptions{})
	forbidden("B begins a multipart into A", err)
	_, err = b.List(ctx, "ws/wsA/", objectstore.ListOptions{})
	forbidden("B lists A's prefix", err)
	// Prefix-smuggling attempts: a sibling workspace sharing a string prefix, dot segments, the bare prefix.
	for _, k := range []string{"ws/wsAB/x", "ws/wsB/../wsA/secret", "ws/wsB", "ws/wsB/", "wsA/secret", "other/x"} {
		if _, _, err := b.Get(ctx, k, nil); !errors.Is(err, objectstore.ErrForbidden) && !errors.Is(err, objectstore.ErrInvalidKey) {
			t.Errorf("Get(%q) by B: got %v, want ErrForbidden/ErrInvalidKey", k, err)
		}
	}
	// Listing with no prefix means "my own", never everything.
	page, err := b.List(ctx, "", objectstore.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Key != "ws/wsB/own" {
		t.Fatalf("B's unscoped listing = %+v", page.Objects)
	}
	// A's bytes are untouched and A still has full access.
	if got, _ := readAll(t, a, "ws/wsA/secret", nil); string(got) != "A's bytes" {
		t.Fatalf("A's object changed: %q", got)
	}
	for _, bad := range []string{"", "a/b", "..", "x\\y"} {
		if _, err := objectstore.ForWorkspace(base, bad); err == nil {
			t.Errorf("ForWorkspace(%q) accepted a bad workspace id", bad)
		}
	}
}

func ctxCancel(t *testing.T, cfg Config) {
	s := cfg.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Put(ctx, "ws/a/cancelled", strings.NewReader("x"), objectstore.PutOptions{}); err == nil {
		t.Fatal("Put ignored a cancelled context")
	}
	if _, err := s.Head(context.Background(), "ws/a/cancelled"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("a cancelled Put left an object")
	}
}

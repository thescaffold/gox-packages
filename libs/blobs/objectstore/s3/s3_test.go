package s3_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/blobs/objectstore"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/s3"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/s3/s3test"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/storetest"
)

var secret = []byte("s3-driver-test-secret-0123456789")

const (
	ak, sk, region, bucket = "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "eu-west-1", "test-bucket"
)

func newStore(t *testing.T, srv *s3test.Server, mod func(*s3.Config)) *s3.Store {
	t.Helper()
	cfg := s3.Config{
		Endpoint: srv.URL, Region: region, Bucket: bucket, AccessKeyID: ak, SecretAccessKey: sk,
		PresignSecret: secret, MaxObjectSize: 8 << 20, PartSize: 1 << 20,
	}
	if mod != nil {
		mod(&cfg)
	}
	s, err := s3.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The S3 driver against the shared contract suite, served by an in-memory S3.
func TestContract(t *testing.T) {
	const max = 8 << 20
	storetest.Run(t, storetest.Config{
		New: func(t *testing.T) objectstore.ObjectStore {
			srv := s3test.New(bucket, ak, sk, region)
			t.Cleanup(srv.Close)
			return newStore(t, srv, nil)
		},
		PresignSecret: secret,
		MaxObjectSize: max,
		LargeBytes:    max,
	})
}

func TestContract_WithKeyPrefix(t *testing.T) {
	storetest.Run(t, storetest.Config{
		New: func(t *testing.T) objectstore.ObjectStore {
			srv := s3test.New(bucket, ak, sk, region)
			t.Cleanup(srv.Close)
			return newStore(t, srv, func(c *s3.Config) { c.Prefix = "tenant-data/blobs" })
		},
		PresignSecret: secret,
		MaxObjectSize: 8 << 20,
		LargeBytes:    4 << 20,
	})
}

func TestBadCredentialsAreRejected(t *testing.T) {
	srv := s3test.New(bucket, ak, sk, region)
	defer srv.Close()
	s := newStore(t, srv, func(c *s3.Config) { c.SecretAccessKey = "not-the-secret" })
	if _, err := s.Put(context.Background(), "ws/a/x", strings.NewReader("x"), objectstore.PutOptions{}); err == nil {
		t.Fatal("a store with the wrong secret wrote an object")
	}
}

func TestFailedAndAbandonedPutsLeaveNoUpload(t *testing.T) {
	srv := s3test.New(bucket, ak, sk, region)
	defer srv.Close()
	s := newStore(t, srv, func(c *s3.Config) { c.PartSize = 64 << 10 })
	ctx := context.Background()

	big := bytes.Repeat([]byte("z"), 300<<10) // five parts
	if _, err := s.Put(ctx, "ws/a/bad-sum", bytes.NewReader(big), objectstore.PutOptions{ExpectedSHA256: strings.Repeat("0", 64)}); !errors.Is(err, objectstore.ErrChecksumMismatch) {
		t.Fatalf("checksum: %v", err)
	}
	boom := errors.New("connection reset")
	if _, err := s.Put(ctx, "ws/a/broken", io.MultiReader(bytes.NewReader(big[:200<<10]), errReader{boom}), objectstore.PutOptions{}); !errors.Is(err, boom) {
		t.Fatalf("reader failure: %v", err)
	}
	s2 := newStore(t, srv, func(c *s3.Config) { c.PartSize = 64 << 10; c.MaxObjectSize = 100 << 10 })
	if _, err := s2.Put(ctx, "ws/a/too-big", bytes.NewReader(big), objectstore.PutOptions{}); !errors.Is(err, objectstore.ErrTooLarge) {
		t.Fatalf("size limit: %v", err)
	}
	if srv.OpenUploads() != 0 || srv.Count() != 0 {
		t.Fatalf("left behind: %d open uploads, %d objects", srv.OpenUploads(), srv.Count())
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestPresignPut_DirectUploadToTheBucket(t *testing.T) {
	srv := s3test.New(bucket, ak, sk, region)
	defer srv.Close()
	s := newStore(t, srv, nil)
	ctx := context.Background()

	u, err := s.PresignPut(ctx, "uploads/item-1/abc", "application/pdf", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	put := func(url, ct string, body string) int {
		req, _ := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	// A browser sends no credentials, only the URL and the content type it was issued for.
	if got := put(u, "application/pdf", "%PDF-1.4 hello"); got != 200 {
		t.Fatalf("direct PUT = %d", got)
	}
	info, err := s.Head(ctx, "uploads/item-1/abc")
	if err != nil || info.Size != 14 || info.MediaType != "application/pdf" {
		t.Fatalf("uploaded object: %+v %v", info, err)
	}
	// The URL is bound to its content type and to its key.
	if got := put(u, "text/html", "<script>"); got != 403 {
		t.Fatalf("a different content type was accepted (%d)", got)
	}
	if got := put(strings.Replace(u, "item-1/abc", "item-2/abc", 1), "application/pdf", "x"); got != 403 {
		t.Fatalf("the URL signed for one key wrote another (%d)", got)
	}
	// It expires, and a too-long lifetime is clamped to 15 minutes.
	srv.Now = func() time.Time { return time.Now().Add(20 * time.Minute) }
	if got := put(u, "application/pdf", "late"); got != 403 {
		t.Fatalf("an expired URL was accepted (%d)", got)
	}
	long, _ := s.PresignPut(ctx, "uploads/item-1/long", "text/plain", 48*time.Hour)
	srv.Now = func() time.Time { return time.Now().Add(16 * time.Minute) }
	if got := put(long, "text/plain", "x"); got != 403 {
		t.Fatalf("a 48h request outlived the 15 minute clamp (%d)", got)
	}
}

func TestConditionalPutUsesTheBucketNotARace(t *testing.T) {
	srv := s3test.New(bucket, ak, sk, region)
	defer srv.Close()
	s := newStore(t, srv, func(c *s3.Config) { c.PartSize = 64 << 10 })
	ctx := context.Background()
	big := bytes.Repeat([]byte("q"), 200<<10)
	if _, err := s.Put(ctx, "ws/a/once", bytes.NewReader(big), objectstore.PutOptions{IfNotExists: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "ws/a/once", bytes.NewReader(big), objectstore.PutOptions{IfNotExists: true}); !errors.Is(err, objectstore.ErrExists) {
		t.Fatalf("second multipart IfNotExists: %v", err)
	}
	if srv.OpenUploads() != 0 {
		t.Fatal("the rejected upload was not aborted")
	}
}

func TestNewValidates(t *testing.T) {
	for name, c := range map[string]s3.Config{
		"no bucket":    {AccessKeyID: "a", SecretAccessKey: "b", PresignSecret: secret},
		"no keys":      {Bucket: "b", PresignSecret: secret},
		"short secret": {Bucket: "b", AccessKeyID: "a", SecretAccessKey: "b", PresignSecret: []byte("short")},
		"bad endpoint": {Bucket: "b", AccessKeyID: "a", SecretAccessKey: "b", PresignSecret: secret, Endpoint: "ftp://x"},
	} {
		if _, err := s3.New(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

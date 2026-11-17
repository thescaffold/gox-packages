package s3_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/thescaffold/gox-packages/libs/blobs/objectstore"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/s3"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/s3/s3test"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/storetest"
)

// A service that keeps no part checksums (Ceph RGW before Squid, e.g. Linode Object Storage)
// holds to the whole contract too: CompleteMultipart checks the parts against UploadPart's
// markers instead of ListParts' ChecksumSHA256.
func TestContract_NoPartChecksums(t *testing.T) {
	for name, prefix := range map[string]string{"bucket root": "", "key prefix": "tenant-data/blobs"} {
		t.Run(name, func(t *testing.T) {
			storetest.Run(t, storetest.Config{
				New: func(t *testing.T) objectstore.ObjectStore {
					srv := s3test.New(bucket, ak, sk, region)
					srv.NoPartChecksums = true
					t.Cleanup(srv.Close)
					return newStore(t, srv, func(c *s3.Config) { c.Prefix = prefix })
				},
				PresignSecret: secret,
				MaxObjectSize: 8 << 20,
				LargeBytes:    8 << 20,
			})
		})
	}
}

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func markerKeys(srv *s3test.Server) []string {
	var out []string
	for _, k := range srv.Keys() {
		if strings.Contains(k, ".os-multipart/") {
			out = append(out, k)
		}
	}
	return out
}

func TestPartMarkers(t *testing.T) {
	srv := s3test.New(bucket, ak, sk, region)
	srv.NoPartChecksums = true
	defer srv.Close()
	s := newStore(t, srv, func(c *s3.Config) { c.Prefix = "p" })
	ctx := context.Background()
	if _, err := s.Put(ctx, "ws/a/plain", strings.NewReader("plain"), objectstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}

	p1, p2 := bytes.Repeat([]byte("1"), 1000), []byte("two")
	id, err := s.BeginMultipart(ctx, "ws/a/mp", objectstore.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	i1, err := s.UploadPart(ctx, id, 1, bytes.NewReader(p1))
	if err != nil {
		t.Fatal(err)
	}
	i2, err := s.UploadPart(ctx, id, 2, bytes.NewReader(p2))
	if err != nil {
		t.Fatal(err)
	}
	if m := markerKeys(srv); len(m) != 2 || !strings.HasPrefix(m[0], "p/.os-multipart/") {
		t.Fatalf("one marker per part, under the store's prefix: %v", m)
	}

	// List never shows the markers, on any page.
	var listed []string
	after := ""
	for {
		page, err := s.List(ctx, "", objectstore.ListOptions{After: after, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range page.Objects {
			listed = append(listed, o.Key)
		}
		if page.Next == "" {
			break
		}
		after = page.Next
	}
	if strings.Join(listed, ",") != "ws/a/plain" {
		t.Fatalf("listed %v", listed)
	}

	// A part listed with another SHA-256, or a part never uploaded, is refused; the upload stays.
	bad := i1
	bad.SHA256 = sha([]byte("something else"))
	if _, err := s.CompleteMultipart(ctx, id, []objectstore.PartInfo{bad, i2}); !errors.Is(err, objectstore.ErrInvalidPart) {
		t.Fatalf("a wrong SHA-256: %v", err)
	}
	if _, err := s.CompleteMultipart(ctx, id, []objectstore.PartInfo{i1, i2, {Number: 3, Size: 1, SHA256: sha([]byte("x"))}}); !errors.Is(err, objectstore.ErrInvalidPart) {
		t.Fatalf("a part never uploaded: %v", err)
	}
	// A part uploaded again replaces its marker: the first upload's SHA-256 no longer matches.
	p1b := bytes.Repeat([]byte("b"), 1000)
	i1b, err := s.UploadPart(ctx, id, 1, bytes.NewReader(p1b))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteMultipart(ctx, id, []objectstore.PartInfo{i1, i2}); !errors.Is(err, objectstore.ErrInvalidPart) {
		t.Fatalf("the replaced part's old SHA-256: %v", err)
	}
	if srv.OpenUploads() != 1 || len(markerKeys(srv)) != 2 {
		t.Fatalf("a refused completion changed the upload: %d open, markers %v", srv.OpenUploads(), markerKeys(srv))
	}

	// The right list completes, and the markers go.
	info, err := s.CompleteMultipart(ctx, id, []objectstore.PartInfo{i1b, i2})
	if err != nil {
		t.Fatal(err)
	}
	if info.SHA256 != sha(append(append([]byte{}, p1b...), p2...)) {
		t.Fatalf("completed %+v", info)
	}
	if m := markerKeys(srv); len(m) != 0 {
		t.Fatalf("markers left after completion: %v", m)
	}

	// An abort removes them too.
	id2, _ := s.BeginMultipart(ctx, "ws/a/mp2", objectstore.PutOptions{})
	if _, err := s.UploadPart(ctx, id2, 1, strings.NewReader("abandoned")); err != nil {
		t.Fatal(err)
	}
	if len(markerKeys(srv)) != 1 {
		t.Fatalf("markers %v", markerKeys(srv))
	}
	if err := s.AbortMultipart(ctx, id2); err != nil {
		t.Fatal(err)
	}
	if m := markerKeys(srv); len(m) != 0 || srv.OpenUploads() != 0 {
		t.Fatalf("after abort: markers %v, %d open uploads", m, srv.OpenUploads())
	}
}

// A service that keeps part checksums (S3, MinIO) gets no markers: ListParts is the check.
func TestNoMarkersWhereTheServiceKeepsChecksums(t *testing.T) {
	srv := s3test.New(bucket, ak, sk, region)
	defer srv.Close()
	s := newStore(t, srv, nil)
	ctx := context.Background()
	id, _ := s.BeginMultipart(ctx, "ws/a/mp", objectstore.PutOptions{})
	pi, err := s.UploadPart(ctx, id, 1, strings.NewReader("one"))
	if err != nil {
		t.Fatal(err)
	}
	if m := markerKeys(srv); len(m) != 0 {
		t.Fatalf("markers written: %v", m)
	}
	bad := pi
	bad.SHA256 = sha([]byte("not one"))
	if _, err := s.CompleteMultipart(ctx, id, []objectstore.PartInfo{bad}); !errors.Is(err, objectstore.ErrInvalidPart) {
		t.Fatalf("a wrong SHA-256: %v", err)
	}
	if _, err := s.CompleteMultipart(ctx, id, []objectstore.PartInfo{pi}); err != nil {
		t.Fatal(err)
	}
}

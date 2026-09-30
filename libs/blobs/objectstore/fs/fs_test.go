package fs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/blobs/objectstore"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/fs"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/storetest"
)

var secret = []byte("fs-driver-test-secret-0123456789")

// The fs driver against the shared contract suite (PLAN M1-05).
func TestContract(t *testing.T) {
	const max = 8 << 20
	storetest.Run(t, storetest.Config{
		New: func(t *testing.T) objectstore.ObjectStore {
			s, err := fs.New(t.TempDir(), fs.Config{MaxObjectSize: max, PresignSecret: secret})
			if err != nil {
				t.Fatal(err)
			}
			return s
		},
		PresignSecret: secret,
		MaxObjectSize: max,
		LargeBytes:    max,
	})
}

func TestSweepUploads_RemovesOnlyStaleUploads(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	s, _ := fs.New(dir, fs.Config{PresignSecret: secret, Now: func() time.Time { return now }})
	old, _ := s.BeginMultipart(t.Context(), "ws/a/old", objectstore.PutOptions{})
	fresh, _ := s.BeginMultipart(t.Context(), "ws/a/fresh", objectstore.PutOptions{})
	past := now.Add(-3 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "mp", string(old)), past, past); err != nil {
		t.Fatal(err)
	}
	n, err := s.SweepUploads(time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("swept %d, %v; want exactly the stale one", n, err)
	}
	if _, err := s.UploadPart(t.Context(), fresh, 1, strings.NewReader("x")); err != nil {
		t.Fatalf("the fresh upload was swept: %v", err)
	}
}

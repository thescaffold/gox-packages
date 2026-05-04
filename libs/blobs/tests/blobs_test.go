package tests

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages-blobs"
	"github.com/thescaffold/gox-packages-blobs/files"
)

func TestBlobs(t *testing.T) {
	test.NewSuiteRunner(t, &BlobsSuite{}).Run()
}

type BlobsSuite struct {
	test.Suite
}

// ── LocalProvider ──────────────────────────────────────────────────────────────

func (s *BlobsSuite) TestLocalProvider_Upload_CreatesFile() {
	dir, _ := os.MkdirTemp("", "blobs-test-*")
	p := files.NewLocalProvider(dir)
	r := strings.NewReader("hello world")
	url, err := p.Upload(r, "test.txt", "bucket1", nil)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(url).ToEqual("/storage/bucket1/test.txt")
	_, statErr := os.Stat(filepath.Join(dir, "bucket1", "test.txt"))
	s.T.Expect(statErr).ToBeNil()
}

func (s *BlobsSuite) TestLocalProvider_Download_ReadsFile() {
	dir, _ := os.MkdirTemp("", "blobs-test-*")
	p := files.NewLocalProvider(dir)
	// Write a file first
	_ = os.MkdirAll(filepath.Join(dir, "bucket1"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bucket1", "hello.txt"), []byte("content"), 0o644)

	rc, err := p.Download("bucket1/hello.txt")
	s.T.Expect(err).ToBeNil()
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	s.T.Expect(string(data)).ToEqual("content")
}

func (s *BlobsSuite) TestLocalProvider_Download_MissingFile_ReturnsError() {
	dir, _ := os.MkdirTemp("", "blobs-test-*")
	p := files.NewLocalProvider(dir)
	_, err := p.Download("bucket1/nope.txt")
	s.T.Expect(err == nil).ToEqual(false)
}

// ── S3Provider stub ────────────────────────────────────────────────────────────

func (s *BlobsSuite) TestS3Provider_Upload_ReturnsError() {
	p := &files.S3Provider{}
	_, err := p.Upload(strings.NewReader("x"), "f.txt", "b", nil)
	s.T.Expect(err == nil).ToEqual(false)
}

func (s *BlobsSuite) TestS3Provider_Download_ReturnsError() {
	p := &files.S3Provider{}
	_, err := p.Download("anything")
	s.T.Expect(err == nil).ToEqual(false)
}

// ── FilesService ───────────────────────────────────────────────────────────────

func (s *BlobsSuite) TestFilesService_UploadReader_DelegatesToProvider() {
	dir, _ := os.MkdirTemp("", "blobs-test-*")
	svc := files.NewFilesService(files.NewLocalProvider(dir))
	url, err := svc.UploadReader(strings.NewReader("data"), "doc.txt", "docs", nil)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(url).ToEqual("/storage/docs/doc.txt")
}

// ── BlobsModule ────────────────────────────────────────────────────────────────

func (s *BlobsSuite) TestRegister_Local_ReturnsModule() {
	m := blobs.Register(blobs.BlobsConfig{Provider: blobs.ProviderLocal})
	s.T.Expect(m == nil).ToEqual(false)
}

func (s *BlobsSuite) TestDeclarations_Local_HasTwoEntries() {
	m := blobs.Register(blobs.BlobsConfig{Provider: blobs.ProviderLocal})
	s.T.Expect(len(m.Declarations())).ToEqual(2)
}

func (s *BlobsSuite) TestDeclarations_S3_HasTwoEntries() {
	m := blobs.Register(blobs.BlobsConfig{Provider: blobs.ProviderS3})
	s.T.Expect(len(m.Declarations())).ToEqual(2)
}

func (s *BlobsSuite) TestExports_MatchDeclarations() {
	m := blobs.Register(blobs.BlobsConfig{Provider: blobs.ProviderLocal})
	s.T.Expect(len(m.Exports())).ToEqual(len(m.Declarations()))
}

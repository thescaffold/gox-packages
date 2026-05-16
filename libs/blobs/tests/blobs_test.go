package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages/libs/blobs"
	"github.com/thescaffold/gox-packages/libs/blobs/files"
	corehttp "github.com/thescaffold/gox-packages/libs/core/http"
)

func TestBlobs(t *testing.T) {
	test.NewSuiteRunner(t, &BlobsSuite{}).Run()
}

type BlobsSuite struct {
	test.Suite
}

// scaffoldRecorder is a stub of the scaffold blobs server.
// It records every request, replays canned responses, and counts batch chunks.
type scaffoldRecorder struct {
	*httptest.Server
	authHeader string
	initBody   map[string]any
	verifyBody map[string]any
	chunkCount int
	chunks     []string
}

func newScaffoldRecorder() *scaffoldRecorder {
	r := &scaffoldRecorder{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "" {
			r.authHeader = req.Header.Get("Authorization")
		}
		w.Header().Set("Content-Type", "application/json")

		// jsx-blobs init()/batch()/verify() read the raw HTTP body directly
		// (no `data` envelope unwrap), so the server replies with the File /
		// result object at the top level.
		switch req.URL.Path {
		case "/apps/blobs/upload/init":
			_ = json.NewDecoder(req.Body).Decode(&r.initBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"id":     "file-123",
			})
		case "/apps/blobs/upload/batch":
			var body struct {
				Pages []map[string]any `json:"pages"`
			}
			_ = json.NewDecoder(req.Body).Decode(&body)
			r.chunkCount += len(body.Pages)
			for _, p := range body.Pages {
				if raw, ok := p["raw"].(string); ok {
					r.chunks = append(r.chunks, raw)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":   "success",
				"received": len(body.Pages),
			})
		case "/apps/blobs/upload/verify":
			_ = json.NewDecoder(req.Body).Decode(&r.verifyBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":     "success",
				"id":         "file-123",
				"url":        r.URL + "/storage/file-123",
				"pagesCount": r.chunkCount,
			})
		default:
			http.NotFound(w, req)
		}
	}))
	return r
}

// ── Module registration ───────────────────────────────────────────────────────

func (s *BlobsSuite) TestRegister_ValidConfig_ReturnsModule() {
	m := blobs.Register(blobs.BlobsConfig{
		Server: "http://example.test", Credential: "tok", SourceId: "src",
	})
	s.T.Expect(m == nil).ToEqual(false)
}

func (s *BlobsSuite) TestRegister_MissingServer_Panics() {
	defer func() {
		s.T.Expect(recover() == nil).ToEqual(false)
	}()
	blobs.Register(blobs.BlobsConfig{Credential: "t", SourceId: "src"})
}

func (s *BlobsSuite) TestRegister_MissingSourceId_Panics() {
	defer func() {
		s.T.Expect(recover() == nil).ToEqual(false)
	}()
	blobs.Register(blobs.BlobsConfig{Server: "http://x", Credential: "t"})
}

func (s *BlobsSuite) TestDeclarations_OneEntry() {
	m := blobs.Register(blobs.BlobsConfig{
		Server: "http://example.test", Credential: "tok", SourceId: "src",
	})
	s.T.Expect(len(m.Declarations())).ToEqual(1)
}

func (s *BlobsSuite) TestExports_MatchDeclarations() {
	m := blobs.Register(blobs.BlobsConfig{
		Server: "http://example.test", Credential: "tok", SourceId: "src",
	})
	s.T.Expect(len(m.Exports())).ToEqual(len(m.Declarations()))
}

// ── FilesService.Download ─────────────────────────────────────────────────────

func (s *BlobsSuite) TestDownload_ReturnsServerURL() {
	svc := files.NewFilesService(files.Config{
		Server: "http://example.test", Credential: "tok", SourceId: "src",
	}, corehttp.New(""))
	url := svc.Download("abc")
	s.T.Expect(url).ToEqual("http://example.test/apps/blobs/download/abc")
}

// jsx-blobs download() concatenates `${config.server}/apps/blobs/download/<id>`
// with no trailing-slash trimming — a server URL with a trailing slash yields
// a doubled slash, exactly as in jsx.
func (s *BlobsSuite) TestDownload_NoTrailingSlashTrim() {
	svc := files.NewFilesService(files.Config{
		Server: "http://example.test/", Credential: "tok", SourceId: "src",
	}, corehttp.New(""))
	url := svc.Download("abc")
	s.T.Expect(url).ToEqual("http://example.test//apps/blobs/download/abc")
}

// ── FilesService.Upload (drives init→batch→verify) ────────────────────────────

func (s *BlobsSuite) TestUpload_DrivesInitBatchVerify() {
	srv := newScaffoldRecorder()
	defer srv.Close()
	svc := files.NewFilesService(files.Config{
		Server: srv.URL, Credential: "the-token", SourceId: "src-1",
	}, corehttp.New(""))

	dir, _ := os.MkdirTemp("", "blobs-up-*")
	path := filepath.Join(dir, "hello.txt")
	_ = os.WriteFile(path, []byte("hello world"), 0o644)

	ok, urlOrErr := svc.Upload(path, "parent-1", []string{"a", "b"})
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(strings.HasPrefix(urlOrErr, srv.URL+"/storage/")).ToEqual(true)

	s.T.Expect(strings.HasPrefix(srv.authHeader, "bearer ")).ToEqual(true)
	s.T.Expect(srv.authHeader).ToEqual("bearer the-token")

	s.T.Expect(srv.initBody["name"]).ToEqual("hello.txt")
	s.T.Expect(srv.initBody["type"]).ToEqual("txt")
	s.T.Expect(srv.initBody["mime"]).ToEqual("text/plain")
	s.T.Expect(srv.initBody["parentId"]).ToEqual("parent-1")

	s.T.Expect(srv.chunkCount > 0).ToEqual(true)
	s.T.Expect(srv.verifyBody["name"]).ToEqual("hello.txt")
}

func (s *BlobsSuite) TestUpload_MissingFile_ReturnsFalse() {
	srv := newScaffoldRecorder()
	defer srv.Close()
	svc := files.NewFilesService(files.Config{
		Server: srv.URL, Credential: "tok", SourceId: "src",
	}, corehttp.New(""))
	ok, msg := svc.Upload("/no/such/file.txt", "", nil)
	s.T.Expect(ok).ToEqual(false)
	s.T.Expect(msg == "").ToEqual(false)
}

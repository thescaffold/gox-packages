package files

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	corehttp "github.com/thescaffold/gox-packages/libs/core/http"
)

// chunkSize matches jsx-blobs streamFileChunks default of 512 KiB.
const chunkSize = 512 * 1024

// Config is the subset of BlobsConfig that FilesService needs.
type Config struct {
	Server     string
	Credential string
	SourceId   string
}

// File mirrors jsx-blobs File interface.
type File struct {
	Type     string   `json:"type"`
	ParentID string   `json:"parentId,omitempty"`
	Name     string   `json:"name"`
	Tags     []string `json:"tags,omitempty"`
	Size     int64    `json:"size"`
	Mime     string   `json:"mime"`
	Status   string   `json:"status,omitempty"`
	Meta     any      `json:"meta,omitempty"`

	// ID is populated by the server response (init / verify).
	ID  string `json:"id,omitempty"`
	URL string `json:"url,omitempty"`
	// PagesCount is populated by verify.
	PagesCount int `json:"pagesCount,omitempty"`
}

// Page mirrors jsx-blobs Page interface.
type Page struct {
	FileID string `json:"fileId"`
	Index  int    `json:"index"`
	Raw    string `json:"raw"`

	Status string `json:"status,omitempty"`
	Meta   any    `json:"meta,omitempty"`
}

// FilesService is a thin wrapper around the scaffold blobs HTTP API.
// Mirrors jsx-packages/libs/blobs/src/files/index.ts.
type FilesService struct {
	cfg    Config
	client *corehttp.Client
}

// NewFilesService creates a FilesService that POSTs to cfg.Server using cfg.Credential.
func NewFilesService(cfg Config, client *corehttp.Client) *FilesService {
	if client == nil {
		client = corehttp.New("")
	}
	return &FilesService{cfg: cfg, client: client}
}

// Upload reads the file at path, uploads its bytes in chunks, and returns
// (success, urlOrErrorMessage) — exactly like jsx-blobs upload().
func (s *FilesService) Upload(input, parentID string, tags []string) (bool, string) {
	ext, mime, size, name, err := getFileMeta(input)
	if err != nil {
		return false, "Failed to read file metadata"
	}
	if ext == "" || mime == "" || size == 0 || name == "" {
		return false, "Invalid file metadata"
	}

	created, err := s.init(File{
		Type:     ext,
		ParentID: parentID,
		Name:     name,
		Tags:     tags,
		Size:     size,
		Mime:     mime,
	})
	if err != nil || created == nil || created.ID == "" {
		return false, "Failed to init file upload"
	}

	totalChunks, err := streamFileChunks(input, func(chunk string, index int) error {
		_, batchErr := s.batch([]Page{{FileID: created.ID, Index: index, Raw: chunk}})
		return batchErr
	})
	if err != nil || totalChunks < 1 {
		return false, "Failed to upload file chunks"
	}

	verified, err := s.verify(File{
		Type:     ext,
		ParentID: parentID,
		Name:     name,
		Tags:     tags,
		Size:     size,
		Mime:     mime,
	})
	if err != nil || verified == nil || verified.URL == "" {
		return false, "Failed to verify file upload"
	}
	if verified.PagesCount != totalChunks {
		return false, "Uploaded chunks count does not match"
	}

	return true, verified.URL
}

// Download returns the full URL for the given file ID. Matches jsx-blobs download().
func (s *FilesService) Download(id string) string {
	return fmt.Sprintf("%s/apps/blobs/download/%s", strings.TrimRight(s.cfg.Server, "/"), id)
}

// init posts file metadata and returns the server's File response (with id).
func (s *FilesService) init(file File) (*File, error) {
	url := fmt.Sprintf("%s/apps/blobs/upload/init", strings.TrimRight(s.cfg.Server, "/"))
	return s.postFile(url, file)
}

// batch posts one or more chunked pages.
func (s *FilesService) batch(pages []Page) (any, error) {
	url := fmt.Sprintf("%s/apps/blobs/upload/batch", strings.TrimRight(s.cfg.Server, "/"))
	body := map[string]any{"pages": pages}
	return s.postEnvelope(url, body)
}

// verify finalizes the upload and returns the File with url + pagesCount.
func (s *FilesService) verify(file File) (*File, error) {
	url := fmt.Sprintf("%s/apps/blobs/upload/verify", strings.TrimRight(s.cfg.Server, "/"))
	return s.postFile(url, file)
}

// postFile posts a File and decodes the response body's `data` into a File.
func (s *FilesService) postFile(url string, file File) (*File, error) {
	data, err := s.postEnvelope(url, file)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	out := &File{}
	if err := json.Unmarshal(bytes, out); err != nil {
		return nil, err
	}
	return out, nil
}

// postEnvelope wraps the http.Client POST + bearer auth + envelope unwrap.
// Returns the `data` field of the response envelope, mirroring TS post() callers
// that return `response?.data`.
func (s *FilesService) postEnvelope(url string, body any) (any, error) {
	headers := map[string]string{
		"authorization": "bearer " + s.cfg.Credential,
		"content-type":  "application/json",
	}
	ok, status, statusText, _, resp := s.client.Request("POST", url, body, nil, headers, 0)
	if !ok {
		return nil, fmt.Errorf("blobs: %d %s", status, statusText)
	}
	// Envelope shape: { status, title, message, data, meta, raw, headers }
	if env, isMap := resp.(map[string]any); isMap {
		return env["data"], nil
	}
	return resp, nil
}

// streamFileChunks reads path in 512 KiB chunks, base64-encodes each, and invokes cb.
// Returns total chunks emitted. Mirrors jsx-blobs streamFileChunksForNode.
func streamFileChunks(path string, cb func(chunk string, index int) error) (int, error) {
	if path == "" {
		return 0, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	buf := make([]byte, chunkSize)
	index := 0
	for {
		n, err := f.Read(buf)
		if n > 0 {
			encoded := base64.StdEncoding.EncodeToString(buf[:n])
			if cbErr := cb(encoded, index); cbErr != nil {
				return index, cbErr
			}
			index++
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return index, err
		}
	}
	return index, nil
}

// getFileMeta returns (ext, mime, size, name, err) for the given path.
// Mirrors jsx-blobs getFileMetaForNode.
func getFileMeta(path string) (string, string, int64, string, error) {
	if path == "" {
		return "", "", 0, "", fmt.Errorf("blobs: empty path")
	}
	stat, err := os.Stat(path)
	if err != nil {
		return "", "", 0, "", err
	}
	name := filepath.Base(path)
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	mime := mimeMap[ext]
	if mime == "" {
		mime = "application/octet-stream"
	}
	return ext, mime, stat.Size(), name, nil
}

package files

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LocalProvider stores files on the local filesystem.
// URL format: /storage/{bucket}/{name}
// BaseDir is the root directory for all stored files (default: "./storage").
type LocalProvider struct {
	BaseDir string
}

func NewLocalProvider(baseDir string) *LocalProvider {
	if baseDir == "" {
		baseDir = "./storage"
	}
	return &LocalProvider{BaseDir: baseDir}
}

func (p *LocalProvider) Upload(r io.Reader, name, bucket string, tags []string) (string, error) {
	dir := filepath.Join(p.BaseDir, bucket)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("blobs: create dir: %w", err)
	}
	dest := filepath.Join(dir, name)
	f, err := os.Create(dest)
	if err != nil {
		return "", fmt.Errorf("blobs: create file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return "", fmt.Errorf("blobs: write file: %w", err)
	}
	return "/storage/" + bucket + "/" + name, nil
}

func (p *LocalProvider) Download(id string) (io.ReadCloser, error) {
	// id is expected to be the relative path: bucket/name
	path := filepath.Join(p.BaseDir, id)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("blobs: open file: %w", err)
	}
	return f, nil
}

package i18n

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
)

// Loader can load raw YAML text by path (e.g. "translations/en/ntx/users.yaml").
type Loader interface {
	Load(path string) (string, error)
}

// FileLoader reads translation files from a local base directory.
type FileLoader struct {
	BaseDir string // e.g. "/app/translations" — files are resolved as BaseDir/path
}

func (f *FileLoader) Load(path string) (string, error) {
	full := f.BaseDir + "/" + path
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("i18n: %w", err)
	}
	return string(data), nil
}

// HTTPLoader fetches translation files from a remote base URL with an in-memory cache.
type HTTPLoader struct {
	BaseURL string
	mu      sync.RWMutex
	cache   map[string]string
}

func NewHTTPLoader(baseURL string) *HTTPLoader {
	return &HTTPLoader{BaseURL: baseURL, cache: map[string]string{}}
}

func (h *HTTPLoader) Load(path string) (string, error) {
	h.mu.RLock()
	if v, ok := h.cache[path]; ok {
		h.mu.RUnlock()
		return v, nil
	}
	h.mu.RUnlock()

	url := h.BaseURL + "/" + path
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("i18n: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("i18n: %w", err)
	}
	content := string(data)

	h.mu.Lock()
	h.cache[path] = content
	h.mu.Unlock()
	return content, nil
}

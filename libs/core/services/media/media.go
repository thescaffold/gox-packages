// Package media ports ntx-packages/libs/core/src/services/media.service.ts.
// MediaService loads remote text files (e.g. translation YAML) and caches them
// in memory.
package media

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Service caches remote text files. Concurrent-safe.
type Service struct {
	baseURL string
	client  *http.Client
	mu      sync.RWMutex
	store   map[string]string
}

// New constructs a Media Service rooted at baseURL.
func New(baseURL string) *Service {
	return &Service{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
		store:   map[string]string{},
	}
}

// Load fetches each path (relative to baseURL) and caches the body.
// Already-cached paths are skipped.
func (s *Service) Load(paths []string) error {
	for _, path := range paths {
		s.mu.RLock()
		_, has := s.store[path]
		s.mu.RUnlock()
		if has {
			continue
		}
		body, err := s.fetch(path)
		if err != nil {
			// match TS: log + continue, do not fail the batch
			continue
		}
		s.mu.Lock()
		s.store[path] = body
		s.mu.Unlock()
	}
	return nil
}

// Get returns the cached body for path, or "" if not loaded.
func (s *Service) Get(path string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store[path]
}

// LoadAndGet ensures path is cached, then returns the body. Mirrors TS
// loadAndGet(): it calls Load (which swallows fetch errors) then Get, so a
// failed fetch yields "" rather than an error.
func (s *Service) LoadAndGet(path string) string {
	_ = s.Load([]string{path})
	return s.Get(path)
}

func (s *Service) fetch(path string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, s.baseURL+"/"+strings.TrimLeft(path, "/"), nil)
	if err != nil {
		return "", err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("media: %s -> %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// Package services hosts the suite of platform services ported from
// ntx-packages/libs/core/src/services. They share a CacheBackend interface so
// callers can wire any backing store (Redis, in-memory, Memcached) without
// changing the service surface.
package services

import (
	"sync"
	"time"
)

// CacheBackend is the minimal cache contract every service depends on.
// Implementations: services/cache.MemoryBackend, redis-backed, etc.
type CacheBackend interface {
	// Get returns the value for key, or "" + ok=false if missing/expired.
	Get(key string) (string, bool)
	// Set stores value under key for ttl. ttl=0 means no expiry.
	Set(key, value string, ttl time.Duration)
	// Del removes a key.
	Del(key string)
	// Setnx sets the key only if it does not already exist.
	// Returns true when the key was set.
	Setnx(key, value string, ttl time.Duration) bool
	// Getset atomically gets the previous value and sets a new one.
	Getset(key, value string, ttl time.Duration) string
	// Incr atomically increments the integer value at key (treating missing as 0).
	// Returns the new value.
	Incr(key string) int64
	// TTL returns the remaining lifetime of a key, or 0 if missing/no-expiry.
	TTL(key string) time.Duration
}

// MemoryBackend is a goroutine-safe in-memory CacheBackend. Use in single-process
// deployments and tests; replace with Redis for clustered deployments.
type MemoryBackend struct {
	mu    sync.Mutex
	store map[string]memEntry
}

type memEntry struct {
	value     string
	expiresAt time.Time // zero = no expiry
}

// NewMemoryBackend creates an empty in-memory cache.
func NewMemoryBackend() *MemoryBackend { return &MemoryBackend{store: map[string]memEntry{}} }

func (m *MemoryBackend) get(key string, now time.Time) (memEntry, bool) {
	e, ok := m.store[key]
	if !ok {
		return memEntry{}, false
	}
	if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
		delete(m.store, key)
		return memEntry{}, false
	}
	return e, true
}

func (m *MemoryBackend) Get(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.get(key, time.Now())
	return e.value, ok
}

func (m *MemoryBackend) Set(key, value string, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.set(key, value, ttl)
}

func (m *MemoryBackend) set(key, value string, ttl time.Duration) {
	e := memEntry{value: value}
	if ttl > 0 {
		e.expiresAt = time.Now().Add(ttl)
	}
	m.store[key] = e
}

func (m *MemoryBackend) Del(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.store, key)
}

func (m *MemoryBackend) Setnx(key, value string, ttl time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.get(key, time.Now()); ok {
		return false
	}
	m.set(key, value, ttl)
	return true
}

func (m *MemoryBackend) Getset(key, value string, ttl time.Duration) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := ""
	if e, ok := m.get(key, time.Now()); ok {
		prev = e.value
	}
	m.set(key, value, ttl)
	return prev
}

func (m *MemoryBackend) Incr(key string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := int64(0)
	if e, ok := m.get(key, time.Now()); ok {
		var n int64
		for _, c := range e.value {
			if c < '0' || c > '9' {
				cur = 0
				break
			}
			n = n*10 + int64(c-'0')
			cur = n
		}
	}
	cur++
	prev, _ := m.store[key]
	prev.value = formatInt(cur)
	if prev.expiresAt.IsZero() && false {
		// preserve missing expiry
	}
	m.store[key] = prev
	return cur
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func (m *MemoryBackend) TTL(key string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.get(key, time.Now())
	if !ok || e.expiresAt.IsZero() {
		return 0
	}
	d := time.Until(e.expiresAt)
	if d < 0 {
		return 0
	}
	return d
}

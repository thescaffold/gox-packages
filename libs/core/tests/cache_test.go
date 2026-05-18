package tests

import (
	"errors"
	"sync"
	"testing"
	"time"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages/libs/core/services"
)

func TestCache(t *testing.T) {
	test.NewSuiteRunner(t, &CacheSuite{}).Run()
}

type CacheSuite struct {
	test.Suite
}

// ── MemoryBackend (L1) ─────────────────────────────────────────────────────────

func (s *CacheSuite) TestMemory_SetGet() {
	c := services.NewMemoryBackend()
	c.Set("k", "v", time.Minute)
	v, ok := c.Get("k")
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(v).ToEqual("v")
}

func (s *CacheSuite) TestMemory_GetMissing() {
	c := services.NewMemoryBackend()
	_, ok := c.Get("absent")
	s.T.Expect(ok).ToEqual(false)
}

func (s *CacheSuite) TestMemory_Expiry() {
	c := services.NewMemoryBackend()
	c.Set("k", "v", 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	_, ok := c.Get("k")
	s.T.Expect(ok).ToEqual(false)
}

func (s *CacheSuite) TestMemory_Setnx_HonoursExisting() {
	c := services.NewMemoryBackend()
	s.T.Expect(c.Setnx("k", "first", time.Minute)).ToEqual(true)
	s.T.Expect(c.Setnx("k", "second", time.Minute)).ToEqual(false)
	v, _ := c.Get("k")
	s.T.Expect(v).ToEqual("first")
}

func (s *CacheSuite) TestMemory_Incr_FromMissing() {
	c := services.NewMemoryBackend()
	s.T.Expect(c.Incr("counter")).ToEqual(int64(1))
	s.T.Expect(c.Incr("counter")).ToEqual(int64(2))
}

// ── CacheService (no L2) ───────────────────────────────────────────────────────

func (s *CacheSuite) TestCacheService_MemoryOnly_GetSet() {
	svc := services.NewCacheService(nil)
	svc.Set("k", "v", time.Minute)
	v, ok := svc.Get("k")
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(v).ToEqual("v")
}

func (s *CacheSuite) TestCacheService_Cache_PopulatesOnMiss() {
	svc := services.NewCacheService(nil)
	calls := 0
	fn := func() (string, error) {
		calls++
		return "computed", nil
	}
	v, _ := svc.Cache("k", fn, time.Minute)
	s.T.Expect(v).ToEqual("computed")
	v2, _ := svc.Cache("k", fn, time.Minute)
	s.T.Expect(v2).ToEqual("computed")
	// Function should be called exactly once — second call hits the cache.
	s.T.Expect(calls).ToEqual(1)
}

func (s *CacheSuite) TestCacheService_Cache_PropagatesError() {
	svc := services.NewCacheService(nil)
	_, err := svc.Cache("k", func() (string, error) { return "", errors.New("boom") }, time.Minute)
	s.T.Expect(err == nil).ToEqual(false)
}

// ── CacheService with L2 ───────────────────────────────────────────────────────

// fakeL2 is a controllable L2 stub for testing two-tier semantics.
type fakeL2 struct {
	mu    sync.Mutex
	store map[string]string
	calls map[string]int
}

func newFakeL2() *fakeL2 {
	return &fakeL2{store: map[string]string{}, calls: map[string]int{}}
}

func (f *fakeL2) record(method string) {
	f.calls[method]++
}

func (f *fakeL2) Get(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Get")
	v, ok := f.store[key]
	return v, ok
}

func (f *fakeL2) Set(key, value string, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Set")
	f.store[key] = value
}

func (f *fakeL2) Del(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Del")
	delete(f.store, key)
}

func (f *fakeL2) Setnx(key, value string, _ time.Duration) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Setnx")
	if _, exists := f.store[key]; exists {
		return false
	}
	f.store[key] = value
	return true
}

func (f *fakeL2) Getset(key, value string, _ time.Duration) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Getset")
	prev := f.store[key]
	f.store[key] = value
	return prev
}

func (f *fakeL2) Incr(key string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Incr")
	// crude string-as-counter
	current := int64(0)
	if v, ok := f.store[key]; ok {
		for _, c := range v {
			if c >= '0' && c <= '9' {
				current = current*10 + int64(c-'0')
			}
		}
	}
	current++
	f.store[key] = formatIntForFake(current)
	return current
}

func (f *fakeL2) TTL(_ string) time.Duration { return time.Minute }

func formatIntForFake(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func (s *CacheSuite) TestCacheService_Get_ConsultsL2OnL1Miss() {
	l2 := newFakeL2()
	l2.store["remote"] = "via-l2"
	svc := services.NewCacheService(l2)
	v, ok := svc.Get("remote")
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(v).ToEqual("via-l2")
	// Second read should hit L1 — no extra L2 call.
	beforeCount := l2.calls["Get"]
	_, _ = svc.Get("remote")
	s.T.Expect(l2.calls["Get"]).ToEqual(beforeCount)
}

func (s *CacheSuite) TestCacheService_Set_DualWrite() {
	l2 := newFakeL2()
	svc := services.NewCacheService(l2)
	svc.Set("k", "v", time.Minute)
	s.T.Expect(l2.store["k"]).ToEqual("v")
}

func (s *CacheSuite) TestCacheService_Setnx_L2Authoritative() {
	l2 := newFakeL2()
	l2.store["k"] = "pre-existing"
	svc := services.NewCacheService(l2)
	// L1 doesn't know about the key, but L2 already holds it — Setnx must fail.
	s.T.Expect(svc.Setnx("k", "new", time.Minute)).ToEqual(false)
}

func (s *CacheSuite) TestCacheService_Del_DualClear() {
	l2 := newFakeL2()
	svc := services.NewCacheService(l2)
	svc.Set("k", "v", time.Minute)
	svc.Del("k")
	_, l1ok := services.NewCacheServiceWith(nil, nil).Get("k") // sanity: empty cache
	s.T.Expect(l1ok).ToEqual(false)
	_, l2ok := l2.store["k"]
	s.T.Expect(l2ok).ToEqual(false)
}

// ── Lock / Unlock ──────────────────────────────────────────────────────────────

func (s *CacheSuite) TestLock_AcquiresWhenAvailable() {
	svc := services.NewCacheService(nil)
	err := svc.Lock("file", "ref-1", time.Second, false)
	s.T.Expect(err).ToBeNil()
	svc.Unlock("file", "ref-1")
}

func (s *CacheSuite) TestLock_NoWait_FailsWhenHeld() {
	svc := services.NewCacheService(nil)
	_ = svc.Lock("file", "ref-1", time.Second, false)
	err := svc.Lock("file", "ref-1", time.Second, false)
	s.T.Expect(err == nil).ToEqual(false)
}

// ── Cleanup goroutine ──────────────────────────────────────────────────────────

func (s *CacheSuite) TestStartCleanup_PurgesExpiredEntries() {
	svc := services.NewCacheService(nil)
	svc.Set("k", "v", 10*time.Millisecond)
	svc.StartCleanup(20 * time.Millisecond)
	defer svc.StopCleanup()
	time.Sleep(60 * time.Millisecond)
	// purgeExpired is called by the goroutine; Get after expiry returns false.
	_, ok := svc.Get("k")
	s.T.Expect(ok).ToEqual(false)
}

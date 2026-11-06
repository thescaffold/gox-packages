package throttler

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thescaffold/gox-packages/libs/core/services"
)

// Concurrent requests must not get past the limit: the count is taken and
// compared in one step, not read, compared and then incremented.
func TestThrottleHoldsTheLimitUnderConcurrency(t *testing.T) {
	s := New(services.NewMemoryBackend(), time.Minute, 10)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if ok, _ := s.Throttle("u1", 1); ok {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if allowed.Load() != 10 {
		t.Fatalf("%d requests allowed, want exactly 10", allowed.Load())
	}
	if ok, ttl := s.Throttle("u1", 1); ok || ttl <= 0 {
		t.Fatalf("after the limit: allowed=%v ttl=%v", ok, ttl)
	}
	if ok, _ := s.Throttle("u2", 1); !ok {
		t.Fatal("another user is throttled")
	}
}

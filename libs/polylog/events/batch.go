package events

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	corehttp "github.com/thescaffold/gox-packages-core/http"
)

// batchLimit matches jsx-polylog's `limit = 25` chunk size in request/batch.ts.
const batchLimit = 25

// BatchConfig matches jsx-polylog Config.batch.
type BatchConfig struct {
	// Interval is the flush tick duration. Default: 5 seconds.
	Interval time.Duration
	// Backoff is the sleep duration after a failed flush. Default: 1 second.
	Backoff time.Duration
	// Limit is the maximum number of consecutive retries before the flusher stops. Default: 3.
	Limit int
}

// FlusherConfig is the config slice the Flusher needs.
type FlusherConfig struct {
	Server     string
	Credential string
	Batch      BatchConfig
}

// Flusher periodically POSTs queued items to /apps/polylog/ingest/batch.
// Mirrors jsx-polylog request/batch.ts run() loop.
type Flusher struct {
	cfg     FlusherConfig
	queue   *Queue
	client  *corehttp.Client
	cancel  context.CancelFunc
	stopped chan struct{}
	mu      sync.Mutex
	running bool
}

// NewFlusher constructs a Flusher. Defaults are applied to cfg.Batch when zero.
func NewFlusher(cfg FlusherConfig, queue *Queue, client *corehttp.Client) *Flusher {
	if cfg.Batch.Interval <= 0 {
		cfg.Batch.Interval = 5 * time.Second
	}
	if cfg.Batch.Backoff <= 0 {
		cfg.Batch.Backoff = time.Second
	}
	if cfg.Batch.Limit <= 0 {
		cfg.Batch.Limit = 3
	}
	if client == nil {
		client = corehttp.New("")
	}
	return &Flusher{cfg: cfg, queue: queue, client: client, stopped: make(chan struct{})}
}

// Run starts the background flush loop. Safe to call once; subsequent calls no-op.
func (f *Flusher) Run() {
	f.mu.Lock()
	if f.running {
		f.mu.Unlock()
		return
	}
	f.running = true
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.mu.Unlock()

	go f.loop(ctx)
}

// Stop signals the flush loop to exit. Returns once the goroutine has finished.
func (f *Flusher) Stop() {
	f.mu.Lock()
	if !f.running {
		f.mu.Unlock()
		return
	}
	f.running = false
	cancel := f.cancel
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	<-f.stopped
}

func (f *Flusher) loop(ctx context.Context) {
	defer close(f.stopped)

	ticker := time.NewTicker(f.cfg.Batch.Interval)
	defer ticker.Stop()

	retries := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if f.queue.Len() < 1 {
			continue
		}

		items := f.queue.Pop(batchLimit)
		ok, err := f.flush(items)
		if ok && err == nil {
			retries = 0
			continue
		}

		// On failure, push items back and apply backoff.
		f.queue.Push(items...)
		retries++
		if retries > f.cfg.Batch.Limit {
			// Retry limit exhausted — stop processing.
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(f.cfg.Batch.Backoff):
		}
	}
}

// flush POSTs items to /apps/polylog/ingest/batch.
func (f *Flusher) flush(items []Item) (bool, error) {
	if len(items) == 0 {
		return true, nil
	}
	url := fmt.Sprintf("%s/apps/polylog/ingest/batch", strings.TrimRight(f.cfg.Server, "/"))
	headers := map[string]string{
		"authorization": "bearer " + f.cfg.Credential,
		"content-type":  "application/json",
	}
	body := map[string]any{"items": items}
	ok, status, statusText, _, _ := f.client.Request("POST", url, body, nil, headers, 0)
	if !ok {
		return false, fmt.Errorf("polylog batch: %d %s", status, statusText)
	}
	return true, nil
}

// Package ratelimit limits requests per caller (U-S25): a fixed-window counter
// per key, and a middleware that answers over the limit with 429, a stable
// code, Retry-After, and X-RateLimit-* headers on every limited response.
//
// The host decides who a request counts against and how much it may do (Rule):
// a token's id, a person, an address. Counts live in a Store: MemoryStore for
// one process, SQLStore (Postgres) when several processes serve the same
// callers. A Store that fails lets the request through: a broken counter must
// not take the service down.
package ratelimit

import (
	"context"
	"database/sql"
	"log"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/awesome-goose/goose/types"

	"github.com/thescaffold/gox-packages/libs/core/response"
)

// Code is the stable error code of a refused request.
const Code = "rate_limited"

// Limit is how many requests a key may make per window.
type Limit struct {
	Requests int64
	Window   time.Duration
}

// Store counts hits per key and window.
type Store interface {
	// Hit counts one request for key in the window starting at start and
	// lasting window, and returns the count including this one.
	Hit(ctx context.Context, key string, start time.Time, window time.Duration) (int64, error)
}

// Rule names the caller a request counts against and its limit; ok=false
// leaves the request unlimited.
type Rule func(ctx types.Context) (key string, limit Limit, ok bool)

// Middleware applies a Rule. Now is a seam for tests.
type Middleware struct {
	Store Store
	Rule  Rule
	Now   func() time.Time
	// Title is the response title of a refusal ("" means "Too many requests").
	Title string
}

var _ types.Middleware = (*Middleware)(nil)

// Result is one request's standing in its window.
type Result struct {
	Limit     int64
	Remaining int64
	Reset     time.Time // when the window ends
	Allowed   bool
}

// Headers are the X-RateLimit-* headers for r, plus Retry-After when refused.
func (r Result) Headers(now time.Time) map[string]string {
	h := map[string]string{
		"X-RateLimit-Limit":     strconv.FormatInt(r.Limit, 10),
		"X-RateLimit-Remaining": strconv.FormatInt(r.Remaining, 10),
		"X-RateLimit-Reset":     strconv.FormatInt(r.Reset.Unix(), 10),
	}
	if !r.Allowed {
		h["Retry-After"] = strconv.FormatInt(int64(math.Ceil(r.Reset.Sub(now).Seconds())), 10)
	}
	return h
}

// Check counts a request for key against l at now.
func Check(ctx context.Context, s Store, key string, l Limit, now time.Time) (Result, error) {
	start := now.Truncate(l.Window)
	n, err := s.Hit(ctx, key, start, l.Window)
	if err != nil {
		return Result{}, err
	}
	rem := l.Requests - n
	if rem < 0 {
		rem = 0
	}
	return Result{Limit: l.Requests, Remaining: rem, Reset: start.Add(l.Window), Allowed: n <= l.Requests}, nil
}

func (m *Middleware) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Middleware) Handle(ctx types.Context) error {
	if m.Store == nil || m.Rule == nil {
		return nil
	}
	key, l, ok := m.Rule(ctx)
	if !ok || key == "" || l.Requests <= 0 || l.Window <= 0 {
		return nil
	}
	now := m.now()
	r, err := Check(context.Background(), m.Store, key, l, now)
	if err != nil {
		log.Printf("ratelimit: %v (request allowed)", err)
		return nil
	}
	h := r.Headers(now)
	ctx.Response().SetHeaders(h)
	if r.Allowed {
		return nil
	}
	title := m.Title
	if title == "" {
		title = "Too many requests"
	}
	msg := "too many requests; try again in " + h["Retry-After"] + " seconds"
	return &types.Respond{Output: withHeaders(response.ErrorCode(title, msg, 429, Code), h)}
}

// withHeaders adds headers to an output (the kernel writes an output's own
// headers, so a refusal carries them even if the response's were reset).
type headered struct {
	types.Output
	h map[string]string
}

func (o headered) Headers() map[string]string {
	out := map[string]string{}
	for k, v := range o.Output.Headers() {
		out[k] = v
	}
	for k, v := range o.h {
		out[k] = v
	}
	return out
}

func withHeaders(o types.Output, h map[string]string) types.Output { return headered{Output: o, h: h} }

// MemoryStore counts in this process only.
type MemoryStore struct {
	mu     sync.Mutex
	counts map[string]memCount
	hits   int
}

type memCount struct {
	start time.Time
	n     int64
	until time.Time
}

// NewMemoryStore makes an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{counts: map[string]memCount{}} }

func (s *MemoryStore) Hit(_ context.Context, key string, start time.Time, window time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.counts[key]
	if !c.start.Equal(start) {
		c = memCount{start: start}
	}
	c.n++
	c.until = start.Add(window)
	s.counts[key] = c
	s.hits++
	if s.hits%1024 == 0 {
		for k, v := range s.counts {
			if v.until.Before(start) {
				delete(s.counts, k)
			}
		}
	}
	return c.n, nil
}

// SQLStore counts in Postgres, shared by every process, with one atomic
// statement per request. Migrate creates its table (UNLOGGED: counts are
// disposable, and not writing them to the WAL keeps the cost low).
type SQLStore struct {
	DB   *sql.DB
	mu   sync.Mutex
	hits int
}

// Migrate creates the counter table.
func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE UNLOGGED TABLE IF NOT EXISTS "CoreRateLimits" (
		"key"          varchar(255) NOT NULL,
		"window_start" timestamptz  NOT NULL,
		"count"        bigint       NOT NULL,
		"expires_at"   timestamptz  NOT NULL,
		PRIMARY KEY ("key", "window_start")
	)`)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS "idx_core_rate_limits_expiry" ON "CoreRateLimits" ("expires_at")`)
	return err
}

func (s *SQLStore) Hit(ctx context.Context, key string, start time.Time, window time.Duration) (int64, error) {
	var n int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO "CoreRateLimits" ("key", "window_start", "count", "expires_at")
		VALUES ($1, $2, 1, $3)
		ON CONFLICT ("key", "window_start") DO UPDATE SET "count" = "CoreRateLimits"."count" + 1
		RETURNING "count"`, key, start.UTC(), start.Add(window).UTC()).Scan(&n)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.hits++
	sweep := s.hits%1000 == 0
	s.mu.Unlock()
	if sweep {
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM "CoreRateLimits" WHERE "expires_at" < $1`, time.Now().UTC().Add(-time.Minute))
	}
	return n, nil
}

package ratelimit

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/awesome-goose/goose/types"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// testCtx is the smallest types.Context with a response that keeps headers.
type testCtx struct {
	values map[any]any
	res    *testRes
}

type testRes struct{ h map[string]string }

func (r *testRes) Write(types.SerialType, []byte, int) error { return nil }
func (r *testRes) SetHeader(k, v string)                     { r.h[k] = v }
func (r *testRes) SetHeaders(h map[string]string) {
	for k, v := range h {
		r.h[k] = v
	}
}
func (r *testRes) Raw() any { return nil }

func newCtx() *testCtx {
	return &testCtx{values: map[any]any{}, res: &testRes{h: map[string]string{}}}
}
func (c *testCtx) Request() types.Request   { return nil }
func (c *testCtx) Response() types.Response { return c.res }
func (c *testCtx) SetValue(k, v any)        { c.values[k] = v }
func (c *testCtx) GetValue(k any) any       { return c.values[k] }

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func TestMiddlewareCountsAndRefuses(t *testing.T) {
	clk := &clock{t: time.Date(2026, 11, 6, 12, 0, 10, 0, time.UTC)}
	m := &Middleware{
		Store: NewMemoryStore(),
		Now:   clk.now,
		Rule: func(ctx types.Context) (string, Limit, bool) {
			k, _ := ctx.GetValue("who").(string)
			return k, Limit{Requests: 3, Window: time.Minute}, k != ""
		},
	}
	call := func(who string) (*testCtx, error) {
		c := newCtx()
		c.SetValue("who", who)
		return c, m.Handle(c)
	}
	for i, want := range []string{"2", "1", "0"} {
		c, err := call("token:a")
		if err != nil {
			t.Fatalf("request %d refused: %v", i+1, err)
		}
		if c.res.h["X-RateLimit-Limit"] != "3" || c.res.h["X-RateLimit-Remaining"] != want {
			t.Fatalf("request %d headers: %v", i+1, c.res.h)
		}
		if c.res.h["X-RateLimit-Reset"] != "1793966460" { // 12:01:00
			t.Fatalf("reset: %v", c.res.h["X-RateLimit-Reset"])
		}
	}
	c, err := call("token:a")
	var r *types.Respond
	if !errors.As(err, &r) || r.Output.Code() != 429 {
		t.Fatalf("fourth request: %v", err)
	}
	if r.Output.Headers()["Retry-After"] != "50" || c.res.h["Retry-After"] != "50" || r.Output.Headers()["X-RateLimit-Remaining"] != "0" {
		t.Fatalf("refusal headers: %v / %v", r.Output.Headers(), c.res.h)
	}
	// Another caller has their own count; no caller is not limited.
	if _, err := call("token:b"); err != nil {
		t.Fatalf("another caller: %v", err)
	}
	if c, err := call(""); err != nil || len(c.res.h) != 0 {
		t.Fatalf("unlimited request: %v %v", err, c.res.h)
	}
	// The next window starts afresh.
	clk.t = clk.t.Add(time.Minute)
	if c, err := call("token:a"); err != nil || c.res.h["X-RateLimit-Remaining"] != "2" {
		t.Fatalf("next window: %v %v", err, c.res.h)
	}
}

type brokenStore struct{}

func (brokenStore) Hit(context.Context, string, time.Time, time.Duration) (int64, error) {
	return 0, errors.New("down")
}

func TestABrokenStoreLetsRequestsThrough(t *testing.T) {
	m := &Middleware{Store: brokenStore{}, Rule: func(types.Context) (string, Limit, bool) {
		return "k", Limit{Requests: 1, Window: time.Minute}, true
	}}
	for i := 0; i < 3; i++ {
		if err := m.Handle(newCtx()); err != nil {
			t.Fatalf("refused with a broken store: %v", err)
		}
	}
}

// Requires RATELIMIT_TEST_DB_URL (a scratch Postgres database); skips otherwise.
func TestSQLStoreIsExactUnderConcurrency(t *testing.T) {
	dsn := os.Getenv("RATELIMIT_TEST_DB_URL")
	if dsn == "" {
		t.Skip("RATELIMIT_TEST_DB_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	s := &SQLStore{DB: db}
	key := "test:" + time.Now().Format(time.RFC3339Nano)
	now := time.Now()
	l := Limit{Requests: 20, Window: time.Hour}

	var allowed, refused atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := Check(ctx, s, key, l, now)
			if err != nil {
				t.Error(err)
				return
			}
			if r.Allowed {
				allowed.Add(1)
			} else {
				refused.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 20 || refused.Load() != 30 {
		t.Fatalf("allowed %d, refused %d; want exactly 20 and 30", allowed.Load(), refused.Load())
	}
	// A second store (another process) sees the same count.
	other := &SQLStore{DB: db}
	if r, _ := Check(ctx, other, key, l, now); r.Allowed || r.Remaining != 0 {
		t.Fatalf("another process: %+v", r)
	}
	if r, _ := Check(ctx, other, key, l, now.Add(time.Hour)); !r.Allowed || r.Remaining != 19 {
		t.Fatalf("next window: %+v", r)
	}
}

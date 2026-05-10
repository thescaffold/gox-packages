package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages/libs/core/response"
)

// errTooManyRequests is the sentinel returned when the per-user limit is hit.
var errTooManyRequests = errors.New("too many requests")

// ThrottleConfig configures per-user rate limiting.
type ThrottleConfig struct {
	// Limit is the maximum number of requests permitted in the TTL window. Default 60.
	Limit int
	// TTL is the rolling window duration. Default 1 minute.
	TTL time.Duration
}

// UserThrottleGuard rate-limits requests per authenticated user using an
// in-memory rolling window. Place AFTER AuthMiddleware so a user ID is present.
//
// Mirrors ntx-packages/libs/core/src/guards/user-throttle.guard.ts.
type UserThrottleGuard struct {
	cfg     ThrottleConfig
	mu      sync.Mutex
	buckets map[string]*throttleBucket
}

type throttleBucket struct {
	hits []time.Time
}

// NewUserThrottleGuard creates a guard with the given config. Zero values fall
// back to defaults (60 requests / minute).
func NewUserThrottleGuard(cfg ThrottleConfig) *UserThrottleGuard {
	if cfg.Limit <= 0 {
		cfg.Limit = 60
	}
	if cfg.TTL <= 0 {
		cfg.TTL = time.Minute
	}
	return &UserThrottleGuard{cfg: cfg, buckets: map[string]*throttleBucket{}}
}

var _ types.Middleware = (*UserThrottleGuard)(nil)

func (g *UserThrottleGuard) Handle(ctx types.Context) error {
	claims := GetClaims(ctx)
	if claims == nil {
		return writeUnauthorized(ctx)
	}
	userID, _ := claims["sub"].(string)
	if userID == "" {
		userID, _ = claims["userId"].(string)
	}
	if userID == "" {
		userID, _ = claims["id"].(string)
	}
	if userID == "" {
		// no user — let the request through (e.g. service principals).
		return nil
	}

	if g.allow(userID) {
		return nil
	}
	return writeTooManyRequests(ctx)
}

// allow returns true when the userID is below the limit, recording a hit.
func (g *UserThrottleGuard) allow(userID string) bool {
	now := time.Now()
	cutoff := now.Add(-g.cfg.TTL)

	g.mu.Lock()
	defer g.mu.Unlock()

	b, ok := g.buckets[userID]
	if !ok {
		b = &throttleBucket{}
		g.buckets[userID] = b
	}

	// drop expired hits
	idx := 0
	for ; idx < len(b.hits); idx++ {
		if b.hits[idx].After(cutoff) {
			break
		}
	}
	b.hits = b.hits[idx:]

	if len(b.hits) >= g.cfg.Limit {
		return false
	}
	b.hits = append(b.hits, now)
	return true
}

func writeTooManyRequests(ctx types.Context) error {
	env := response.Error("TooManyRequests", "rate limit exceeded", http.StatusTooManyRequests).Data()
	body, _ := json.Marshal(env)
	_ = ctx.Response().Write(types.SerialTypeObject, body, http.StatusTooManyRequests)
	return errTooManyRequests
}

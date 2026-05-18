package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages/libs/core/i18n"
	"github.com/thescaffold/gox-packages/libs/core/response"
	"github.com/thescaffold/gox-packages/libs/core/services/throttler"
	"github.com/thescaffold/gox-packages/libs/core/utils"
)

// errTooManyRequests is the sentinel returned when the per-user limit is hit.
var errTooManyRequests = errors.New("too many requests")

// UserThrottleGuard rate-limits per principal by deferring to ThrottlerService
// (cache-backed). Mirrors ntx-packages/libs/core/src/guards/
// user-throttle.guard.ts exactly:
//
//   - id is taken from claims.id / claims.sub / body.email / body.phoneNumber
//     / body.email_or_phoneNumber (first non-empty wins) — mirroring TS's
//     `req.user?.id ?? req.body?.email ?? req.body?.phoneNumber ?? …`.
//   - throttlerService.throttle(id) returns [allowed, ttl_seconds]; when
//     allowed is false, the guard emits a translated 429 envelope using
//     prettyTimeLeft(ttl * 1000) as the {timeLeft} template variable.
//
// Place AFTER AuthMiddleware so claims are available; the body fallback still
// applies for unauthenticated routes (login/register) where claims are absent.
type UserThrottleGuard struct {
	Throttler *throttler.Service
	Lang      *i18n.Service
}

var _ types.Middleware = (*UserThrottleGuard)(nil)

func (g *UserThrottleGuard) Handle(ctx types.Context) error {
	if g.Throttler == nil {
		return nil
	}

	id := g.extractID(ctx)
	if id == "" {
		// No identifier — let the request through (e.g. health checks).
		return nil
	}

	allowed, ttl := g.Throttler.Throttle(id, 1)
	if allowed {
		return nil
	}

	preference := readPreferenceHeader(ctx)
	title := "Throttle"
	message := "Please try again in a moment"
	if g.Lang != nil {
		title = g.Lang.Translate("packages.core.throttle.title", nil, preference)
		// Mirror TS: prettyTimeLeft is computed against the seconds-based ttl
		// returned by ThrottlerService; the i18n key receives {timeLeft} for
		// templating.
		msMillis := int64(ttl / time.Millisecond)
		// TS passes `ttl * 1000` because ThrottlerService returns seconds.
		// gox's Throttler returns time.Duration, so msMillis is already ms.
		left := utils.PrettyTimeLeft(msMillis)
		message = g.Lang.Translate(
			"packages.core.throttle.service.can-activate.error.enough",
			map[string]any{"timeLeft": left},
			preference,
		)
	}
	return writeTooManyRequests(ctx, title, message)
}

// extractID mirrors TS:
//
//	const user = req?.user ?? req?.raw?.user;
//	const id =
//	  user?.id ?? req?.body?.email ?? req?.body?.phoneNumber ??
//	  req?.body?.phoneNumber ?? req?.body?.email_or_phoneNumber;
//
// claims.id is preferred; sub/userId are tried next as fallbacks before
// touching the request body — matching how the AuthMiddleware populates
// claims in gox (TS sets req.user from passport).
func (g *UserThrottleGuard) extractID(ctx types.Context) string {
	if claims := GetClaims(ctx); claims != nil {
		for _, k := range []string{"id", "sub", "userId"} {
			if v, _ := claims[k].(string); v != "" {
				return v
			}
		}
	}
	// Body fallbacks: parse JSON body once, look up keys in TS order.
	body := readJSONBody(ctx)
	if body != nil {
		for _, k := range []string{"email", "phoneNumber", "email_or_phoneNumber"} {
			if v, _ := body[k].(string); v != "" {
				return v
			}
		}
	}
	return ""
}

func writeTooManyRequests(ctx types.Context, title, message string) error {
	env := response.Error(title, message, http.StatusTooManyRequests).Data()
	body, _ := json.Marshal(env)
	_ = ctx.Response().Write(types.SerialTypeObject, body, http.StatusTooManyRequests)
	return errTooManyRequests
}

// readJSONBody decodes the request body as JSON once and returns the resulting
// map. Returns nil for non-JSON bodies, empty bodies, or parse errors —
// matching TS's `req?.body?.…` which yields undefined in the same cases.
func readJSONBody(ctx types.Context) map[string]any {
	raw, err := ctx.Request().Body()
	if err != nil || len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

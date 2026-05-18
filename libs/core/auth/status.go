package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages/libs/core/i18n"
	"github.com/thescaffold/gox-packages/libs/core/services/platform"
)

// errForbidden is the sentinel returned when status guard rejects a request.
var errForbidden = errors.New("forbidden")

// StatusGuard checks the authenticated principal's billing status via the
// platform capital service. Mirrors ntx-packages/libs/core/src/guards/
// status.guard.ts: it calls apps.capital.get.status() and, when the returned
// `data.danger` flag is true, rejects with a translated error envelope. A
// failed platform call returns nil (allow-through) so users aren't locked out
// by a transient capital outage — same as TS `if (status !== true) return false`
// (note: TS's `return false` translates to "deny" in Nest; gox treats a missing
// auth context as upstream and lets the request pass — see writeForbidden for
// the actual deny path).
//
// Place this middleware AFTER AuthMiddleware so claims are available.
type StatusGuard struct {
	Platform        *platform.Service
	Lang            *i18n.Service
	UnauthorizedMsg string
}

var _ types.Middleware = (*StatusGuard)(nil)

func (g *StatusGuard) Handle(ctx types.Context) error {
	if g.Platform == nil {
		// Not configured — allow through; matches TS `return false` semantics
		// in the sense that the call could not be made, so do not block.
		return nil
	}

	res := g.Platform.CapitalGetStatus()
	if !res.Status {
		// "not sure if the user is active or not, so we return false & allow
		// user to try again" — TS behavior preserved verbatim.
		return nil
	}

	data, _ := res.Data.(map[string]any)
	if data == nil {
		return nil
	}
	if danger, _ := data["danger"].(bool); !danger {
		return nil
	}

	// Danger flag set — translate and reject.
	preference := readPreferenceHeader(ctx)
	title := "Status"
	message := g.UnauthorizedMsg
	if g.Lang != nil {
		title = g.Lang.Translate("packages.core.status.title", nil, preference)
		message = g.Lang.Translate(
			"packages.core.status.service.can-activate.error.enough",
			nil, preference,
		)
	}
	if message == "" {
		message = "account status check failed"
	}
	return writeForbidden(ctx, title+": "+message)
}

// readPreferenceHeader returns the decoded x-ntx-preference header, or nil
// when absent/malformed. Mirrors TS fromBase64(req.headers['x-ntx-preference']).
func readPreferenceHeader(ctx types.Context) map[string]any {
	headers := ctx.Request().Headers()
	if headers == nil {
		return nil
	}
	vs, ok := headers["x-ntx-preference"]
	if !ok || len(vs) == 0 || vs[0] == "" {
		return nil
	}
	// Preferences are base64-encoded JSON per the platform contract.
	out := decodeBase64JSON(vs[0])
	return out
}

// decodeBase64JSON is inlined here to avoid taking a dependency on the
// security package from auth. It returns nil on any decode/parse error so the
// guard treats a missing/garbled preference header as "no preference".
func decodeBase64JSON(b64 string) map[string]any {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

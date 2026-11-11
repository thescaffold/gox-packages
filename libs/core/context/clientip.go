package context

import (
	"os"

	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages/libs/core/utils"
)

// ClientIP is the address of the client behind a request: the connection's peer, unless that
// peer is a proxy listed in TRUSTED_PROXIES (comma-separated CIDRs or addresses, e.g.
// "10.0.0.0/8,127.0.0.1"), in which case X-Forwarded-For / X-Real-Ip say who the client is
// (Goose types.ClientIP). Without TRUSTED_PROXIES no forwarded header is believed, so a client
// cannot pick its own address; behind a proxy, set it, or every caller has the proxy's address.
func ClientIP(ctx types.Context) string {
	if ctx == nil || ctx.Request() == nil {
		return ""
	}
	return types.ClientIP(ctx.Request(), types.ParsePrefixes(os.Getenv("TRUSTED_PROXIES")))
}

// RequestInfo fills the request's own facts into the NTXContext — the client's address (IP) and
// a tracing id when there is none — and keeps everything else already there. It takes nothing
// from x-ntx-* headers, so it is safe on public routes: sign-up, sign-in and recovery need the
// address for their per-address limits even though no session exists yet.
type RequestInfo struct{}

var _ types.Middleware = (*RequestInfo)(nil)

func (m *RequestInfo) Handle(ctx types.Context) error {
	ntx := Get(ctx)
	ntx.IP = ClientIP(ctx)
	if ntx.TracingID == "" {
		ntx.TracingID = utils.Reference("TID", 36)
	}
	Set(ctx, ntx)
	return nil
}

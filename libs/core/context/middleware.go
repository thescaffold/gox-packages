package context

import "github.com/awesome-goose/goose/types"

// Middleware parses x-ntx-* headers on every request and stores the result
// in the goose context for downstream handlers to retrieve via Get().
//
// In addition, when the request carries a ?scope= query parameter, that value
// overrides the header-derived NTXContext.Scope. Mirrors TS
// get-mutable-context.decorator.ts.
type Middleware struct{}

func (m *Middleware) Handle(ctx types.Context) error {
	ntx := Parse(ctx.Request().Headers())
	if override := ctx.Request().Queries()["scope"]; override != "" {
		ntx.Scope = override
	}
	Set(ctx, ntx)
	return nil
}

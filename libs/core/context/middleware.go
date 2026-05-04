package context

import "github.com/awesome-goose/goose/types"

// Middleware parses x-ntx-* headers on every request and stores the result
// in the goose context for downstream handlers to retrieve via Get().
type Middleware struct{}

func (m *Middleware) Handle(ctx types.Context) error {
	ntx := Parse(ctx.Request().Headers())
	Set(ctx, ntx)
	return nil
}

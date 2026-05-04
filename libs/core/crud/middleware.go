package crud

import "github.com/awesome-goose/goose/types"

// QueriesMiddleware stores the full request query-param map in the goose context
// under the key "queries" so that ListDto.Queries (context:"queries") is populated
// by goose's input binder, giving MakeFilter access to all arbitrary filter params.
type QueriesMiddleware struct{}

func (m *QueriesMiddleware) Handle(ctx types.Context) error {
	ctx.SetValue("queries", ctx.Request().Queries())
	return nil
}

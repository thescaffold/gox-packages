package crud

import (
	"errors"

	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/response"
)

// HTTPError lets a BeforeCreate hook or morph reject a request with a
// specific status instead of the generic 500 a plain error becomes — e.g. a
// 404 when a parent id in the body is not in the caller's workspace
// (TRD §7.1 U-S12). The BeforeCreate morph is the right place for such a
// check: it runs on Create, upsert and PUT alike.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string { return e.Message }

// hookFail turns a hook/morph error into the response: its own status for an
// *HTTPError, 500 for anything else.
func (r *CrudResource[E, C, U]) hookFail(err error, ctx ntxctx.NTXContext) types.Output {
	var he *HTTPError
	if errors.As(err, &he) && he.Status >= 400 && he.Status < 600 {
		return response.Error(r.title(ctx), he.Message, he.Status)
	}
	return response.InternalServerError(r.cfg.Name, err.Error())
}

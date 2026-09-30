package crud

import (
	"errors"
	"strings"

	"github.com/awesome-goose/goose/types"
	"github.com/go-playground/validator/v10"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/response"
)

// bindingValidator reads the same `binding:"..."` tags the DTOs already carry.
var bindingValidator = func() *validator.Validate {
	v := validator.New()
	v.SetTagName("binding")
	return v
}()

// validateBody enforces `binding` tags on a DTO when Config.ValidateBody is
// set. It returns nil when the body is acceptable, else a 400 output naming
// every failing field. partial ignores failed `required` rules (PATCH).
func (r *CrudResource[E, C, U]) validateBody(body any, partial bool, ctx ntxctx.NTXContext) types.Output {
	if !r.cfg.ValidateBody {
		return nil
	}
	err := bindingValidator.Struct(body)
	if err == nil {
		return nil
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return response.InternalServerError(r.cfg.Name, err.Error())
	}
	var msgs []string
	for _, fe := range verrs {
		if partial && fe.Tag() == "required" {
			continue
		}
		m := fe.Field() + ": failed '" + fe.Tag() + "'"
		if fe.Param() != "" {
			m += "=" + fe.Param()
		}
		msgs = append(msgs, m)
	}
	if len(msgs) == 0 {
		return nil
	}
	return response.BadRequest(r.title(ctx), strings.Join(msgs, "; "))
}

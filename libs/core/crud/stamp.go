package crud

import (
	"reflect"
	"strings"

	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
)

// stamp makes a write land in the caller's own scope. For a WorkspaceScoped resource (with a
// workspace column, not a WorkspaceScopeClause) it sets the row's workspace column to
// ctx.WorkspaceID, and for an OwnerScoped one the owner column to ctx.UserID, whatever the body
// said: without it a caller scoped to workspace A could create rows in workspace B, or move one
// of their own rows there with an update. ok=false means the caller has no scope to write in,
// and the write must be refused, like scopeWhere's fail-closed reads.
//
// Unscoped resources are untouched.
func (r *CrudResource[E, C, U]) stamp(row *E, ctx ntxctx.NTXContext) bool {
	if r.cfg.WorkspaceScoped && r.cfg.WorkspaceScopeClause == "" {
		if ctx.WorkspaceID == "" {
			return false
		}
		setColumn(row, r.workspaceColumn(), ctx.WorkspaceID)
	}
	if r.cfg.OwnerScoped {
		if ctx.UserID == "" {
			return false
		}
		col := r.cfg.OwnerColumn
		if col == "" {
			col = "user_id"
		}
		setColumn(row, col, ctx.UserID)
	}
	return true
}

// setColumn sets the string or *string field whose gorm column is column (embedded structs
// included). An entity without that column is left as it is.
func setColumn(row any, column, value string) {
	v := reflect.ValueOf(row)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	setColumnIn(v.Elem(), column, value)
}

func setColumnIn(v reflect.Value, column, value string) bool {
	if v.Kind() != reflect.Struct {
		return false
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		if f.Anonymous {
			inner := fv
			if inner.Kind() == reflect.Pointer {
				if inner.IsNil() {
					continue
				}
				inner = inner.Elem()
			}
			if setColumnIn(inner, column, value) {
				return true
			}
			continue
		}
		if !f.IsExported() || gormColumn(f.Tag.Get("gorm")) != column {
			continue
		}
		switch {
		case fv.Kind() == reflect.String:
			fv.SetString(value)
			return true
		case fv.Kind() == reflect.Pointer && fv.Type().Elem().Kind() == reflect.String:
			s := value
			fv.Set(reflect.ValueOf(&s))
			return true
		}
	}
	return false
}

func gormColumn(tag string) string {
	for _, part := range strings.Split(tag, ";") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(part), "column:"); ok {
			return name
		}
	}
	return ""
}

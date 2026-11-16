package crud

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	gsql "github.com/awesome-goose/goose/modules/sql"
	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/response"
)

// The tenant guard (UP-4). A resource is scoped when it confines its rows to
// the caller: WorkspaceScoped, WorkspaceScopeClause, OwnerScoped, or an
// entity that requires a tenant (goose sql.Entity.RequireTenant). Config.Global
// declares a resource that is meant to be shared by every tenant.
//
// With RequireTenantScope(true), a resource that is neither scoped nor Global
// refuses every request (500, packages.core.crud.error.unscoped) instead of
// running a query across every tenant's rows. Hosts turn it on once their
// resources are declared; UnscopedResources lists the ones still missing, for
// a test that keeps the list empty.

var (
	strictTenancy atomic.Bool
	registryMu    sync.Mutex
	registry      = map[string]bool{} // resource → scoped or Global
)

// RequireTenantScope turns the tenant guard on or off for the process.
func RequireTenantScope(on bool) { strictTenancy.Store(on) }

// TenantScopeRequired reports whether the guard is on.
func TenantScopeRequired() bool { return strictTenancy.Load() }

// UnscopedResources lists the hydrated resources that are neither scoped nor
// Global, as "Name (entity type)", sorted.
func UnscopedResources() []string {
	registryMu.Lock()
	defer registryMu.Unlock()
	var out []string
	for name, ok := range registry {
		if !ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// tenantEntity is what a goose sql.Entity (or a type embedding one) offers to
// bind it to a tenant.
type tenantEntity[E any] interface {
	RequiresTenant() (string, bool)
	ForTenant(id any) *gsql.Entity[E]
}

// entityRequiresTenant reports whether the hydrated entity demands a tenant.
func (r *CrudResource[E, C, U]) entityRequiresTenant() bool {
	te, ok := r.entity.(tenantEntity[E])
	if !ok {
		return false
	}
	_, req := te.RequiresTenant()
	return req
}

// scoped reports whether the resource confines rows to the caller.
func (r *CrudResource[E, C, U]) scoped() bool {
	return r.cfg.WorkspaceScoped || r.cfg.WorkspaceScopeClause != "" || r.cfg.OwnerScoped || r.entityRequiresTenant()
}

// register records the resource for UnscopedResources.
func (r *CrudResource[E, C, U]) register() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[fmt.Sprintf("%s (%T)", r.cfg.Name, *new(E))] = r.scoped() || r.cfg.Global
}

// guard refuses the request when the tenant guard is on and the resource is
// unscoped.
func (r *CrudResource[E, C, U]) guard(ctx ntxctx.NTXContext) types.Output {
	if !strictTenancy.Load() || r.cfg.Global || r.scoped() {
		return nil
	}
	return response.InternalServerError(r.title(ctx), r.tr("packages.core.crud.error.unscoped", ctx))
}

// db is the entity every query of this request goes through: bound to the
// caller's workspace when the entity requires a tenant (an empty workspace
// binds nothing, so every query fails closed), the hydrated entity otherwise.
func (r *CrudResource[E, C, U]) db(ctx ntxctx.NTXContext) types.Entity[E] {
	if te, ok := r.entity.(tenantEntity[E]); ok {
		if _, req := te.RequiresTenant(); req {
			return te.ForTenant(ctx.WorkspaceID)
		}
	}
	return r.entity
}

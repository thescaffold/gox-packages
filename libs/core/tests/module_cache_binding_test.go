package tests

import (
	"testing"

	"github.com/awesome-goose/goose/core"
	"github.com/awesome-goose/goose/platforms/cli"
	test "github.com/awesome-goose/goose/testing"
	"github.com/awesome-goose/goose/types"
	coremodule "github.com/thescaffold/gox-packages/libs/core/module"
	"github.com/thescaffold/gox-packages/libs/core/services"
)

// cacheBackendConsumer stands in for any declared struct elsewhere in the
// module tree — e.g. gox-apps/libs/capital's AppService — that depends on
// the cache backend CoreModule is meant to provide, via a plain inject:""
// field typed as the interface (not CoreModule's own concrete type).
type cacheBackendConsumer struct {
	Cache services.CacheBackend `inject:""`
}

// consumerModule imports CoreModule exactly as any app module does via
// module.New(module.CoreConfig{...}), and declares a struct with an
// inject:"" CacheBackend field, mirroring capital's own
// module.New(module.CoreConfig{...}) import alongside its AppService
// declaration.
type consumerModule struct{}

func (m *consumerModule) Imports() []types.Module {
	return []types.Module{coremodule.New(coremodule.CoreConfig{})}
}
func (m *consumerModule) Exports() []any { return nil }
func (m *consumerModule) Declarations() []any {
	return []any{&cacheBackendConsumer{}}
}

func TestCoreModuleCacheBinding(t *testing.T) {
	test.NewSuiteRunner(t, &CoreModuleCacheBindingSuite{}).Run()
}

type CoreModuleCacheBindingSuite struct {
	test.Suite
}

// CoreModule.Declarations() hands its configured (or default in-memory)
// cache out as a concrete *services.MemoryBackend value — visible to
// registry-based lookups (Registry.Get/Resolve) within CoreModule's own
// declarations, but never registered as a container binding under the
// services.CacheBackend interface itself. Any OTHER declared struct
// elsewhere in the tree that wants a plain inject:"" services.CacheBackend
// field (as gox-apps/libs/capital's and notification's AppService both do)
// goes through the registry's r.container.Create(decl) call, which resolves
// inject:"" fields via Container.bindings only (core/container.go's
// create()) — and nothing has ever registered a binding under the
// CacheBackend interface type, so hydration fails the moment it reaches that
// declaration, with CANNOT_CREATE_INTERFACE_FIELD.
func (s *CoreModuleCacheBindingSuite) TestCacheBackendResolvesForADeclaredConsumerElsewhereInTheTree() {
	platform := cli.NewPlatform(cli.WithName("test-cli"))
	instance := &types.Instance{
		Name:     "test-cli",
		Type:     types.PlatformTypeCLI,
		Platform: platform,
		Module:   &consumerModule{},
	}

	k := core.NewKernel()
	// Start() itself still errors once it reaches routing — this module
	// registers no routes — but that must happen only *after* the module
	// tree hydrates successfully. Before the fix, hydration failed first,
	// with CANNOT_CREATE_INTERFACE_FIELD, well before routing was reached.
	_, _ = k.Start(instance)

	decl, err := k.Registry().Get(&cacheBackendConsumer{})
	s.T.Require(err).ToBeNil()

	consumer, ok := decl.Instance.(*cacheBackendConsumer)
	s.T.Require(ok).ToEqual(true)
	s.T.Expect(consumer.Cache).Not().ToBeNil()
}

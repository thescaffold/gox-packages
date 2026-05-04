package module

import (
	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages-core/context"
	"github.com/thescaffold/gox-packages-core/events"
	ntxhttp "github.com/thescaffold/gox-packages-core/http"
	"github.com/thescaffold/gox-packages-core/ws"
)

// CoreConfig holds runtime configuration for the CoreModule.
type CoreConfig struct {
	HMACKey string
}

// CoreModule wires all gox-packages-core services into a single goose module.
// Import it into your app module to gain access to EventBus, TrackerService,
// http.Client, WsService, and the context/crud middleware singletons.
type CoreModule struct {
	cfg CoreConfig
}

// New creates a CoreModule with the given configuration.
func New(cfg CoreConfig) *CoreModule {
	return &CoreModule{cfg: cfg}
}

func (m *CoreModule) Imports() []types.Module { return nil }

func (m *CoreModule) Declarations() []any {
	return []any{
		events.NewBus(),
		&events.TrackerService{},
		ntxhttp.New(m.cfg.HMACKey),
		&ws.WsService{},
		&ntxctx.Middleware{},
	}
}

func (m *CoreModule) Exports() []any {
	return m.Declarations()
}

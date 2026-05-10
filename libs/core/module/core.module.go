package module

import (
	"time"

	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages-core/context"
	"github.com/thescaffold/gox-packages-core/events"
	"github.com/thescaffold/gox-packages-core/filter"
	ntxhttp "github.com/thescaffold/gox-packages-core/http"
	"github.com/thescaffold/gox-packages-core/image"
	"github.com/thescaffold/gox-packages-core/services"
	batchsvc "github.com/thescaffold/gox-packages-core/services/batch"
	markersvc "github.com/thescaffold/gox-packages-core/services/marker"
	otpsvc "github.com/thescaffold/gox-packages-core/services/otp"
	platformsvc "github.com/thescaffold/gox-packages-core/services/platform"
	syncsvc "github.com/thescaffold/gox-packages-core/services/sync"
	textsvc "github.com/thescaffold/gox-packages-core/services/text"
	throttlersvc "github.com/thescaffold/gox-packages-core/services/throttler"
	uaparsersvc "github.com/thescaffold/gox-packages-core/services/uaparser"
	"github.com/thescaffold/gox-packages-core/ws"
)

// CoreConfig holds runtime configuration for the CoreModule.
type CoreConfig struct {
	// HMACKey signs internal HTTP calls.
	HMACKey string
	// BaseURL is the scaffold gateway used by PlatformService.
	BaseURL string
	// Cache is an optional cache backend; an in-memory backend is used when nil.
	Cache services.CacheBackend
	// AuthorizeWS is the JWT validator for incoming WebSocket connections.
	// Pass nil to allow all connections.
	AuthorizeWS ws.AuthorizeFn
	// ThrottleTTL/ThrottleLimit configure the global throttler service.
	ThrottleTTL   time.Duration
	ThrottleLimit int
	// OtpExpiry sets the OtpService expiry. Zero falls back to 7 minutes.
	OtpExpiry time.Duration
}

// CoreModule wires all gox-packages-core services into a single goose module.
// Import it into your app module to gain access to every cross-cutting service.
type CoreModule struct {
	cfg CoreConfig
}

// New creates a CoreModule with the given configuration.
func New(cfg CoreConfig) *CoreModule {
	if cfg.Cache == nil {
		cfg.Cache = services.NewMemoryBackend()
	}
	return &CoreModule{cfg: cfg}
}

func (m *CoreModule) Imports() []types.Module { return nil }

func (m *CoreModule) Declarations() []any {
	bus := events.NewBus()
	tracker := events.NewTrackerService(bus)
	httpClient := ntxhttp.New(m.cfg.HMACKey)
	syncService := syncsvc.New(m.cfg.Cache)

	return []any{
		bus,
		tracker,
		httpClient,
		ws.New(m.cfg.AuthorizeWS),
		&ntxctx.Middleware{},
		filter.NewErrorMiddleware(tracker),
		&image.Service{},
		m.cfg.Cache,
		syncService,
		batchsvc.New(syncService),
		markersvc.New(m.cfg.Cache),
		otpsvc.New(m.cfg.Cache, m.cfg.OtpExpiry),
		platformsvc.New(httpClient, m.cfg.BaseURL),
		textsvc.New(),
		throttlersvc.New(m.cfg.Cache, m.cfg.ThrottleTTL, m.cfg.ThrottleLimit),
		uaparsersvc.New(),
	}
}

func (m *CoreModule) Exports() []any {
	return m.Declarations()
}

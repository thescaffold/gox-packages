package module

import (
	"time"

	"github.com/awesome-goose/goose/types"
	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	"github.com/thescaffold/gox-packages/libs/core/events"
	"github.com/thescaffold/gox-packages/libs/core/filter"
	ntxhttp "github.com/thescaffold/gox-packages/libs/core/http"
	"github.com/thescaffold/gox-packages/libs/core/i18n"
	"github.com/thescaffold/gox-packages/libs/core/image"
	"github.com/thescaffold/gox-packages/libs/core/services"
	batchsvc "github.com/thescaffold/gox-packages/libs/core/services/batch"
	markersvc "github.com/thescaffold/gox-packages/libs/core/services/marker"
	mediasvc "github.com/thescaffold/gox-packages/libs/core/services/media"
	otpsvc "github.com/thescaffold/gox-packages/libs/core/services/otp"
	platformsvc "github.com/thescaffold/gox-packages/libs/core/services/platform"
	syncsvc "github.com/thescaffold/gox-packages/libs/core/services/sync"
	textsvc "github.com/thescaffold/gox-packages/libs/core/services/text"
	throttlersvc "github.com/thescaffold/gox-packages/libs/core/services/throttler"
	uaparsersvc "github.com/thescaffold/gox-packages/libs/core/services/uaparser"
	"github.com/thescaffold/gox-packages/libs/core/ws"
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
	// MediaBaseURL roots the MediaService (MEDIA_BASE_URL) — translation YAML
	// and other remote text assets are fetched relative to it.
	MediaBaseURL string
	// TranslationPaths are i18n YAML paths pre-loaded into the MediaService at
	// boot, mirroring the TS app bootstrap calling mediaService.load(paths).
	// e.g. "translations/en/ntx/apps/assets.yaml".
	TranslationPaths []string
}

// CoreModule wires all gox-packages-core services into a single goose module.
// Import it into your app module to gain access to every cross-cutting service.
type CoreModule struct {
	cfg CoreConfig
	// media and lang are constructed once in New so that Declarations, Exports
	// and Boot all share the same singleton instances.
	media *mediasvc.Service
	lang  *i18n.Service
}

// New creates a CoreModule with the given configuration.
func New(cfg CoreConfig) *CoreModule {
	if cfg.Cache == nil {
		cfg.Cache = services.NewMemoryBackend()
	}
	media := mediasvc.New(cfg.MediaBaseURL)
	// The translation Service reads YAML through the MediaService cache, exactly
	// like the TS translate() reading via mediaService.get(path).
	lang := i18n.NewService(i18n.MediaLoader{Store: media})
	return &CoreModule{cfg: cfg, media: media, lang: lang}
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
		m.media,
		m.lang,
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

// Boot pre-loads the configured translation YAML paths into the MediaService,
// mirroring the TS application bootstrap which calls mediaService.load(paths)
// so that translate() can resolve keys from the in-memory cache. Fetch failures
// are swallowed by MediaService.Load (a missing file degrades to the tail key
// at translate time), so Boot never fails the kernel.
func (m *CoreModule) Boot(_ types.Kernel) error {
	if len(m.cfg.TranslationPaths) > 0 {
		_ = m.media.Load(m.cfg.TranslationPaths)
	}
	return nil
}

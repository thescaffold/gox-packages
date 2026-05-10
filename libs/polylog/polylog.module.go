package polylog

import (
	"errors"
	"time"

	"github.com/awesome-goose/goose/types"
	corehttp "github.com/thescaffold/gox-packages-core/http"
	"github.com/thescaffold/gox-packages-polylog/events"
)

// LogConfig mirrors jsx-polylog Config.log.
type LogConfig struct {
	// Auto, when true, automatically captures log primitives (TS-only behavior).
	Auto bool
}

// EventConfig mirrors jsx-polylog Config.event.
type EventConfig struct {
	// Auto, when true, automatically captures all events. TS only.
	Auto bool
	// Names, when non-nil, filters auto-captured events by name. TS only.
	Names []string
}

// PolylogConfig configures the PolylogModule. Mirrors jsx-polylog Config.
type PolylogConfig struct {
	// Server is the scaffold server base URL (required).
	Server string
	// Credential is the bearer access token (required).
	Credential string
	// SourceId identifies the calling source (required).
	SourceId string
	// Logs filters which log levels are emitted.
	Logs []events.LogType
	// Batch tunes the flusher; zero values fall back to jsx-polylog defaults
	// (5s interval, 1s backoff, 3 retries).
	Batch events.BatchConfig
	// Log/Event are kept for parity with jsx-polylog Config — currently advisory.
	Log   LogConfig
	Event EventConfig
	Debug bool
}

// PolylogModule wires a Queue, EventsService and Flusher.
// Calling Register starts the flusher goroutine. Stop it via the returned
// module's Stop() to clean up the background ticker.
type PolylogModule struct {
	cfg     PolylogConfig
	queue   *events.Queue
	svc     *events.EventsService
	flusher *events.Flusher
}

// Register creates a PolylogModule and starts its background flusher.
// Panics on missing Server or SourceId, matching jsx-polylog init() semantics.
func Register(cfg PolylogConfig) *PolylogModule {
	if err := cfg.validate(); err != nil {
		panic(err)
	}

	queue := events.NewQueue(cfg.SourceId)
	svc := events.New(queue)
	flusher := events.NewFlusher(events.FlusherConfig{
		Server:     cfg.Server,
		Credential: cfg.Credential,
		Batch:      cfg.Batch,
	}, queue, corehttp.New(""))
	flusher.Run()

	return &PolylogModule{cfg: cfg, queue: queue, svc: svc, flusher: flusher}
}

func (cfg PolylogConfig) validate() error {
	if cfg.Server == "" {
		return errors.New("polylog: invalid configuration - server not defined")
	}
	if cfg.SourceId == "" {
		return errors.New("polylog: invalid configuration - sourceId not defined")
	}
	return nil
}

// Stop halts the background flusher. Safe to call multiple times.
func (m *PolylogModule) Stop() { m.flusher.Stop() }

func (m *PolylogModule) Imports() []types.Module { return nil }

func (m *PolylogModule) Declarations() []any {
	return []any{m.queue, m.svc, m.flusher}
}

func (m *PolylogModule) Exports() []any {
	return m.Declarations()
}

// Defaults exposes the documented jsx-polylog defaults so callers can
// inspect them without depending on the `events` subpackage.
var Defaults = struct {
	BatchInterval time.Duration
	BatchBackoff  time.Duration
	BatchLimit    int
}{
	BatchInterval: 5 * time.Second,
	BatchBackoff:  time.Second,
	BatchLimit:    3,
}

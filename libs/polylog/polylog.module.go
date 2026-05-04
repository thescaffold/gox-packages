package polylog

import (
	"github.com/awesome-goose/goose/types"
	coreevents "github.com/thescaffold/gox-packages-core/events"
	"github.com/thescaffold/gox-packages-polylog/events"
)

// PolylogConfig holds configuration for the PolylogModule.
// Extend this struct when adding external analytics sinks (e.g. Segment, Mixpanel).
type PolylogConfig struct{}

// PolylogModule is a goose module that provides analytics event publishing
// over the NTX EventBus. Import it alongside CoreModule in any app that
// needs to emit Identify / Track / Message analytics events.
type PolylogModule struct {
	cfg PolylogConfig
}

// Register creates a PolylogModule with the given configuration.
func Register(cfg PolylogConfig) *PolylogModule {
	return &PolylogModule{cfg: cfg}
}

func (m *PolylogModule) Imports() []types.Module { return nil }

func (m *PolylogModule) Declarations() []any {
	bus := coreevents.NewBus()
	tracker := coreevents.NewTrackerService(bus)
	return []any{
		bus,
		tracker,
		events.New(tracker),
	}
}

func (m *PolylogModule) Exports() []any {
	return m.Declarations()
}

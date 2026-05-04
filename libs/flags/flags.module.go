package flags

import (
	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages-flags/flag"
)

// FlagsConfig configures the FlagsModule.
type FlagsConfig struct {
	// Env is the current environment name used for flag filtering (e.g. "production").
	Env string
}

// FlagsModule is a goose module that provides in-memory feature flag management.
type FlagsModule struct {
	cfg FlagsConfig
}

// Register creates a FlagsModule with the given configuration.
func Register(cfg FlagsConfig) *FlagsModule {
	return &FlagsModule{cfg: cfg}
}

func (m *FlagsModule) Imports() []types.Module { return nil }

func (m *FlagsModule) Declarations() []any {
	return []any{flag.NewFlagService(m.cfg.Env)}
}

func (m *FlagsModule) Exports() []any {
	return m.Declarations()
}

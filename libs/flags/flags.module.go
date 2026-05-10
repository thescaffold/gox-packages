package flags

import (
	"errors"

	"github.com/awesome-goose/goose/types"
	corehttp "github.com/thescaffold/gox-packages-core/http"
	"github.com/thescaffold/gox-packages-flags/flag"
)

// LogType filters which log levels jsx-flags will print.
type LogType string

const (
	LogInfo  LogType = "info"
	LogWarn  LogType = "warn"
	LogError LogType = "error"
)

// FlagsConfig configures the FlagsModule. Mirrors jsx-flags Config.
type FlagsConfig struct {
	Server     string
	Credential string
	SourceId   string
	Logs       []LogType
	Debug      bool
}

// FlagsModule is a goose module that provides feature flag access against a
// remote scaffold flags server.
type FlagsModule struct {
	cfg FlagsConfig
	svc *flag.FlagService
}

// Register creates a FlagsModule with the given configuration.
// Panics on invalid config (missing Server or SourceId), matching jsx-flags
// init() which throws synchronously on the same conditions.
func Register(cfg FlagsConfig) *FlagsModule {
	if err := cfg.validate(); err != nil {
		panic(err)
	}
	svc := flag.NewFlagService(flag.Config{
		Server:     cfg.Server,
		Credential: cfg.Credential,
		SourceId:   cfg.SourceId,
	}, corehttp.New(""))
	return &FlagsModule{cfg: cfg, svc: svc}
}

func (cfg FlagsConfig) validate() error {
	if cfg.Server == "" {
		return errors.New("flags: invalid configuration - server not defined")
	}
	if cfg.SourceId == "" {
		return errors.New("flags: invalid configuration - sourceId not defined")
	}
	return nil
}

func (m *FlagsModule) Imports() []types.Module { return nil }

func (m *FlagsModule) Declarations() []any {
	return []any{m.svc}
}

func (m *FlagsModule) Exports() []any {
	return m.Declarations()
}

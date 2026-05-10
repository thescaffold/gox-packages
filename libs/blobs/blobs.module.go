package blobs

import (
	"errors"

	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages-blobs/files"
	corehttp "github.com/thescaffold/gox-packages-core/http"
)

// LogType filters which log levels jsx-blobs will print.
// Mirrors jsx-packages/libs/blobs/src/common/utils/values.ts LogType.
type LogType string

const (
	LogInfo  LogType = "info"
	LogWarn  LogType = "warn"
	LogError LogType = "error"
)

// BlobsConfig configures the BlobsModule. Mirrors jsx-blobs Config.
type BlobsConfig struct {
	// Server is the destination scaffold server base URL (required).
	Server string
	// Credential is the bearer access token for the scaffold server (required).
	Credential string
	// SourceId identifies the calling source (required).
	SourceId string
	// Logs filters which log levels are emitted (optional).
	Logs []LogType
	// Debug toggles verbose logging (optional).
	Debug bool
}

// BlobsModule is a goose module that wires a FilesService HTTP client.
type BlobsModule struct {
	cfg BlobsConfig
	svc *files.FilesService
}

// Register creates a BlobsModule with the given configuration.
// It panics on invalid config (missing Server or SourceId), matching
// jsx-blobs init() which throws synchronously on the same conditions.
func Register(cfg BlobsConfig) *BlobsModule {
	if err := cfg.validate(); err != nil {
		panic(err)
	}
	client := corehttp.New("")
	svc := files.NewFilesService(files.Config{
		Server:     cfg.Server,
		Credential: cfg.Credential,
		SourceId:   cfg.SourceId,
	}, client)
	return &BlobsModule{cfg: cfg, svc: svc}
}

func (cfg BlobsConfig) validate() error {
	if cfg.Server == "" {
		return errors.New("blobs: invalid configuration - server not defined")
	}
	if cfg.SourceId == "" {
		return errors.New("blobs: invalid configuration - sourceId not defined")
	}
	return nil
}

func (m *BlobsModule) Imports() []types.Module { return nil }

func (m *BlobsModule) Declarations() []any {
	return []any{m.svc}
}

func (m *BlobsModule) Exports() []any {
	return m.Declarations()
}

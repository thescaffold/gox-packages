package blobs

import (
	"github.com/awesome-goose/goose/types"
	"github.com/thescaffold/gox-packages-blobs/files"
)

// ProviderType selects the storage backend.
type ProviderType string

const (
	ProviderLocal ProviderType = "local"
	ProviderS3    ProviderType = "s3"
)

// BlobsConfig configures the BlobsModule.
type BlobsConfig struct {
	Provider ProviderType
	// LocalBaseDir is used when Provider == ProviderLocal (default: "./storage").
	LocalBaseDir string
	// S3* fields are used when Provider == ProviderS3.
	S3Bucket   string
	S3Region   string
	S3Endpoint string
}

// BlobsModule is a goose module that wires a StorageProvider and FilesService.
type BlobsModule struct {
	cfg BlobsConfig
}

// Register creates a BlobsModule with the given configuration.
func Register(cfg BlobsConfig) *BlobsModule {
	return &BlobsModule{cfg: cfg}
}

func (m *BlobsModule) Imports() []types.Module { return nil }

func (m *BlobsModule) Declarations() []any {
	var provider files.StorageProvider
	switch m.cfg.Provider {
	case ProviderS3:
		provider = &files.S3Provider{
			Bucket:   m.cfg.S3Bucket,
			Region:   m.cfg.S3Region,
			Endpoint: m.cfg.S3Endpoint,
		}
	default:
		provider = files.NewLocalProvider(m.cfg.LocalBaseDir)
	}
	svc := files.NewFilesService(provider)
	return []any{provider, svc}
}

func (m *BlobsModule) Exports() []any {
	return m.Declarations()
}

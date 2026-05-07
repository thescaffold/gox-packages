# gox-packages/blobs

Go library for file storage with pluggable provider backends (local filesystem and AWS S3).

## Overview

`blobs` wraps storage provider selection into a single goose module. Register it once in your app module and inject `FilesService` wherever you need to upload or download files.

## Usage

```go
import "github.com/thescaffold/gox-packages-blobs"

// Local storage
blobs.Register(blobs.BlobsConfig{
    Provider:     blobs.ProviderLocal,
    LocalBaseDir: "./storage",
})

// AWS S3
blobs.Register(blobs.BlobsConfig{
    Provider:    blobs.ProviderS3,
    S3Bucket:    "my-bucket",
    S3Region:    "us-east-1",
    S3AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),     // optional: uses default chain if empty
    S3SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
})

// S3-compatible (MinIO)
blobs.Register(blobs.BlobsConfig{
    Provider:   blobs.ProviderS3,
    S3Bucket:   "my-bucket",
    S3Region:   "us-east-1",
    S3Endpoint: "http://minio:9000",
})
```

## API

### `FilesService`

| Method | Signature | Description |
|--------|-----------|-------------|
| `Upload` | `(input io.Reader, parentId string, tags []string) (string, error)` | Store file, return URL |
| `UploadReader` | `(r io.Reader, name, parentId string, tags []string) (string, error)` | Store with explicit name |
| `Download` | `(id string) (io.ReadCloser, error)` | Retrieve file by ID/key |

## Providers

| Provider | Status |
|----------|--------|
| `LocalProvider` | Full implementation — stores to `BaseDir/{bucket}/{name}` |
| `S3Provider` | Full implementation — uses aws-sdk-go-v2 |

## Development

```bash
go test ./...
go build ./...
```

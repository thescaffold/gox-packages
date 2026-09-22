# gox-packages/blobs

Go HTTP client for the scaffold `blobs` server app (`gox-apps/libs/blobs`). It has no storage
logic of its own — every call is a request to a remote scaffold server's `/apps/blobs/*` API.

## Overview

`blobs` wraps that HTTP API into a single goose module. Register it once in your app module and
inject `FilesService` wherever you need to upload a file or link to one.

## Usage

```go
import "github.com/thescaffold/gox-packages/libs/blobs"

blobs.Register(blobs.BlobsConfig{
    Server:     "https://scaffold.example.com", // the blobs server's base URL
    Credential: os.Getenv("BLOBS_CREDENTIAL"),   // bearer token for that server
    SourceId:   "my-app",
})
```

`Register` panics on invalid config (missing `Server` or `SourceId`), matching the JS client's
`init()` throwing synchronously on the same conditions. `Logs` and `Debug` are optional.

## API

### `FilesService`

| Method | Signature | Description |
|--------|-----------|-------------|
| `Upload` | `(path, parentID string, tags []string) (bool, string)` | Reads the file at `path`, uploads it to the server in 512 KiB chunks, and returns `(success, urlOrErrorMessage)` — the error is a message string, not a Go `error`, and a partial-chunk failure does not abort the upload (matches the JS client's behavior). |
| `Download` | `(id string) string` | Returns `<server>/apps/blobs/download/<id>` — a URL, not the file's bytes. Fetching it is the caller's job. |

## Development

```bash
go test ./...
go build ./...
```

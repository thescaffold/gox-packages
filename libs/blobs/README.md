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

## ObjectStore (storage drivers)

Separately from the HTTP client above, this module holds the **`objectstore`**
packages: the one interface everything that stores bytes goes through
(artifacts, exports, evidence, shared prototype files, uploads). The HTTP client
(`FilesService`) is unchanged.

```go
store, _ := backend.Open(ctx, cfg)        // BLOBS_BACKEND=postgres (default) | fs | s3
info, _ := store.Put(ctx, "ws/<workspaceId>/sys/<id>/…", reader, objectstore.PutOptions{MediaType: "application/pdf"})
rc, _, _ := store.Get(ctx, info.Key, &objectstore.Range{Start: 0, End: 1023})
url, _ := store.Presign(ctx, info.Key, objectstore.PresignOptions{Filename: "report.pdf"})
```

| Package | What it is |
|---------|------------|
| `objectstore` | the interface (`Put`, `Get` with byte ranges, `Head`, `Delete`, `List`, `Copy`, `Presign`, multipart), key validation, signed download tokens, `ForWorkspace` tenant confinement |
| `objectstore/pg` | **Postgres driver — the default.** Chunked, AES-256-GCM-sealed `bytea` rows in their own schema; atomic visibility; keyset streaming reads; locked objects enforced by database triggers; quotas; bounded read concurrency; sweeper |
| `objectstore/fs` | local-filesystem driver for tests and single-machine development |
| `objectstore/httpdl` | `GET /api/blobs/dl/<token>`: the download route behind `Presign` (Range, ETag, safe headers, single-use tokens) |
| `objectstore/backend` | `FromEnv` / `Open`: picks the driver from `BLOBS_*` variables |
| `objectstore/storetest` | the driver-agnostic **contract suite**; every driver must pass it |

### Guarantees (enforced by the contract suite)

- **Atomic writes.** A reader sees the previous object or the complete new one; a
  failed, cancelled or killed `Put` leaves nothing visible and cannot damage an
  existing object. A reader that already opened the old object keeps reading its bytes
  intact across an overwrite.
- **Streaming.** Nothing is held whole in memory; a 1 GiB object round-trips with a
  ~50 MiB peak heap (Postgres driver).
- **Tenant boundary.** `ForWorkspace(store, id)` rejects any key outside
  `ws/<id>/` with `ErrForbidden` before it reaches the driver.
- **Immutable evidence.** `PutOptions.Locked` objects refuse overwrite, delete and
  copy-over; the Postgres driver additionally refuses raw `UPDATE`/`DELETE`/`INSERT` on
  their rows via triggers (members of role `blobs_retention`, if you create it, are
  exempt for retention purges). This is tamper *resistance*; tamper *evidence* is the
  SHA-256 in the signed Evidence record.
- **Every object records its size and SHA-256**, computed while writing.

### Configuration

| Variable | Meaning |
|----------|---------|
| `BLOBS_BACKEND` | `postgres` (default), `fs`, or `s3` (not built yet — M7-15a) |
| `BLOBS_MASTER_KEY` | 64 hex characters (32 bytes): root of the envelope-encryption hierarchy (Postgres). Keep it in the secret manager and **back it up — objects cannot be read without it** |
| `BLOBS_PRESIGN_SECRET` | ≥ 16 characters; signs download URLs |
| `BLOBS_FS_ROOT` | directory for the `fs` driver |
| `BLOBS_MAX_OBJECT_SIZE` | per-object cap in bytes (default 100 MiB; larger objects need the S3 backend) |
| `BLOBS_MAX_CONCURRENT_READS` | open downloads per instance (default 16) |
| `BLOBS_SCHEMA` | Postgres schema (default `blobs`) |

Pass the Postgres driver a **dedicated, bounded** `*sql.DB` (`SetMaxOpenConns`) so blob
I/O cannot starve the control plane. Measured: with 16 concurrent 64 MiB downloads
running, a control-plane query's p95 stayed at ~1.5 ms (baseline ~1.8 ms).

### Encryption

Each object has its own random 256-bit data key, wrapped (AES-GCM) under a per-workspace
key derived from `BLOBS_MASTER_KEY`; the master key never reaches the database. Every
chunk is sealed under AAD = object id ‖ position, so a chunk moved to another object or
position is detected, as is any modified byte.

### Operations

- Call `Store.Sweep(ctx)` on a schedule (e.g. every few minutes): it removes unfinished
  uploads older than an hour, purges replaced/deleted objects after a 10-minute grace
  period (so in-flight readers finish), retires expired (`ExpiresAt`) unlocked objects and
  forgets spent single-use tokens.
- Run the tests: `BLOBS_TEST_DB_URL="host=… dbname=…" go test ./objectstore/...`
  (Postgres tests skip without it; `BLOBS_TEST_BIG=1` adds the 1 GiB round trip,
  `BLOBS_TEST_LOAD=1` the concurrent-download load test).

## Development

```bash
go test ./...
go build ./...
```

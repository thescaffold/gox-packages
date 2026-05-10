# gox-packages

Shared Go libraries for Goose-based microservices — the Go rewrite of the `ntx-packages` NestJS library monorepo.

## Overview

`gox-packages` is a Go workspace of four foundational libraries used by every service in [gox-apps](../gox-apps). Each library lives in `libs/<name>/` as an independent Go module with its own `go.mod`.

## Packages

| Package | Module path | Purpose |
|---------|-------------|---------|
| [blobs](libs/blobs/README.md) | `github.com/thescaffold/gox-packages/libs/blobs` | File storage — local filesystem and AWS S3 |
| [core](libs/core/README.md) | `github.com/thescaffold/gox-packages/libs/core` | Auth, CRUD, events, HTTP client, response helpers, utils, image |
| [flags](libs/flags/README.md) | `github.com/thescaffold/gox-packages/libs/flags` | In-memory feature flag management |
| [polylog](libs/polylog/README.md) | `github.com/thescaffold/gox-packages/libs/polylog` | Analytics event tracking (identify/track/message) |

## Repository Structure

```
gox-packages/
├── go.work               # Go workspace linking all library modules
├── go.work.sum
├── libs/
│   ├── blobs/
│   │   ├── go.mod
│   │   ├── blobs.module.go
│   │   └── files/        # StorageProvider interface, LocalProvider, S3Provider
│   ├── core/
│   │   ├── go.mod
│   │   ├── auth/         # JWT sign/verify, AuthMiddleware
│   │   ├── context/      # NTXContext, Middleware
│   │   ├── crud/         # CrudResource generic controller
│   │   ├── events/       # Bus, TrackerService
│   │   ├── filter/       # Error filter middleware
│   │   ├── http/         # Retry HTTP client with HMAC signing
│   │   ├── i18n/         # YAML-based translations
│   │   ├── image/        # SVG generator (solid/gradient/pixel)
│   │   ├── module/       # CoreModule — wires everything
│   │   ├── response/     # Typed response envelope helpers
│   │   ├── security/     # bcrypt, HMAC
│   │   ├── utils/        # UUID, time, money, random, validate
│   │   └── ws/           # WebSocket service
│   ├── flags/
│   │   ├── go.mod
│   │   └── flag/         # FlagService
│   └── polylog/
│       ├── go.mod
│       └── ...           # EventsService
└── Makefile
```

## Getting Started

### Prerequisites

- Go 1.25+

### Build all packages

```bash
go build ./...
```

### Test all packages

```bash
go test ./...
```

## Using in a Service

Add the library to your service's `go.mod`:

```bash
go get github.com/thescaffold/gox-packages/libs/core@latest
```

Import the module in your `AppModule`:

```go
import "github.com/thescaffold/gox-packages/libs/core/module"

func (m *AppModule) Imports() []types.Module {
    return []types.Module{
        module.New(module.CoreConfig{HMACKey: os.Getenv("HMAC_KEY")}),
        // ...
    }
}
```

## Publishing a release

Each lib is a standalone Go module versioned with path-prefixed tags. Always
release `core` first, since the other libs depend on it.

```bash
# 1. Ensure main is clean and pushed
git status
git push origin main

# 2. Tag and push core
git tag libs/core/v0.0.1
git push origin libs/core/v0.0.1

# 3. Tag the dependents (after core is on the remote)
git tag libs/polylog/v0.0.1 libs/blobs/v0.0.1 libs/flags/v0.0.1
git push origin libs/polylog/v0.0.1 libs/blobs/v0.0.1 libs/flags/v0.0.1
```

Consumers then resolve each module independently:

```bash
go get github.com/thescaffold/gox-packages/libs/core@v0.0.1
go get github.com/thescaffold/gox-packages/libs/polylog@v0.0.1
```

For subsequent releases, bump each lib's version independently — e.g. a `core`
patch ships as `libs/core/v0.0.2` without touching the others. Bump the
corresponding `require` line in any dependent lib whose code changed and tag it
too.

### Local development before tags exist

`go.work` includes a `replace` directive so the workspace resolves to the
in-tree copy of `core` even before `libs/core/v0.0.1` is published. This
directive lives only in `go.work` and is not seen by consumers of the published
modules — published `go.mod` files stay clean.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

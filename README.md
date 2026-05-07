# gox-packages

Shared Go libraries for Goose-based microservices — the Go rewrite of the `ntx-packages` NestJS library monorepo.

## Overview

`gox-packages` is a Go workspace of four foundational libraries used by every service in [gox-apps](../gox-apps). Each library lives in `libs/<name>/` as an independent Go module with its own `go.mod`.

## Packages

| Package | Module | Purpose |
|---------|--------|---------|
| [blobs](libs/blobs/README.md) | `gox-packages-blobs` | File storage — local filesystem and AWS S3 |
| [core](libs/core/README.md) | `gox-packages-core` | Auth, CRUD, events, HTTP client, response helpers, utils, image |
| [flags](libs/flags/README.md) | `gox-packages-flags` | In-memory feature flag management |
| [polylog](libs/polylog/README.md) | `gox-packages-polylog` | Analytics event tracking (identify/track/message) |

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

- Go 1.21+

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
go get github.com/thescaffold/gox-packages-core@latest
```

Or, within the workspace, use a `replace` directive:

```
replace github.com/thescaffold/gox-packages-core v0.0.0 => ../gox-packages/libs/core
```

Import the module in your `AppModule`:

```go
import "github.com/thescaffold/gox-packages-core/module"

func (m *AppModule) Imports() []types.Module {
    return []types.Module{
        module.New(module.CoreConfig{HMACKey: os.Getenv("HMAC_KEY")}),
        // ...
    }
}
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

# gox-packages

Shared Go libraries for Goose-based microservices — the Go rewrite of the `ntx-packages` NestJS library monorepo.

## Overview

`gox-packages` is a Go workspace of four foundational libraries used by every service in [gox-apps](../gox-apps). Each library lives in `libs/<name>/` as an independent Go module with its own `go.mod`.

## Packages

| Package                           | Module path                                        | Purpose                                                         |
| --------------------------------- | -------------------------------------------------- | --------------------------------------------------------------- |
| [blobs](libs/blobs/README.md)     | `github.com/thescaffold/gox-packages/libs/blobs`   | File storage — local filesystem and AWS S3                      |
| [core](libs/core/README.md)       | `github.com/thescaffold/gox-packages/libs/core`    | Auth, CRUD, events, HTTP client, response helpers, utils, image |
| [flags](libs/flags/README.md)     | `github.com/thescaffold/gox-packages/libs/flags`   | In-memory feature flag management                               |
| [polylog](libs/polylog/README.md) | `github.com/thescaffold/gox-packages/libs/polylog` | Analytics event tracking (identify/track/message)               |

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

Each lib is a standalone Go module versioned with path-prefixed tags. All four
libs are released together at the same version via:

```bash
make publish version=0.0.1
```

This bumps the `require core` line in `polylog`/`blobs`/`flags` to the new
version, updates the `go.work` replace, commits the change, pushes `main`, then
tags `libs/core/v0.0.1` first (dependents require it) followed by the other
three libs.

The working tree must be clean before publishing — `make publish` aborts if
there are uncommitted changes.

Consumers resolve each module independently:

```bash
go get github.com/thescaffold/gox-packages/libs/core@v0.0.1
go get github.com/thescaffold/gox-packages/libs/polylog@v0.0.1
```

### Releasing a single lib

If only `core` needs a new release (e.g. a patch), invoke the tag flow manually
instead of `make publish`:

```bash
git tag libs/core/v0.0.2
git push origin libs/core/v0.0.2
```

### Local development before tags exist

`go.work` includes a `replace` directive so the workspace resolves to the
in-tree copy of `core` even before `libs/core/v0.0.1` is published. This
directive lives only in `go.work` and is not seen by consumers of the published
modules — published `go.mod` files stay clean.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

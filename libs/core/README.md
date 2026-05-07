# gox-packages/core

Go library of cross-cutting concerns for Goose-based microservices.

## Overview

`core` provides the foundational building blocks shared across all gox services: authentication middleware, CRUD resource factories, HTTP client, event bus, context utilities, response helpers, security primitives, i18n, WebSocket, and common utilities.

## Sub-packages

| Package | Purpose |
|---------|---------|
| `auth` | JWT sign/verify, `AuthMiddleware` (Bearer), scope resolution |
| `context` | Request context extraction (`NTXContext`), `Middleware` |
| `crud` | Generic `CrudResource[E, C, U]` controller factory |
| `events` | In-process wildcard pub/sub `Bus`, `TrackerService` |
| `filter` | Error/panic filter middleware |
| `http` | Retry-capable `Client` with HMAC signing (`Internal`) and plain (`External`) calls |
| `i18n` | YAML-based translation loader and render helper |
| `image` | SVG generator (solid, gradient, pixel variants) |
| `module` | `CoreModule` — wires all above services into a single goose module |
| `response` | Typed response helpers: `Success`, `NotFound`, `Unauthorized`, `Paginated`, etc. |
| `security` | `Hash`/`Compare` (bcrypt), `GenerateHmac` (SHA-256) |
| `utils` | UUID, time, money, random, type coercion, validation helpers |
| `ws` | WebSocket service wrapper |

## Quick Start

```go
import "github.com/thescaffold/gox-packages-core/module"

// In your AppModule.Imports():
module.New(module.CoreConfig{
    HMACKey: os.Getenv("HMAC_KEY"),
})
```

## Auth Middleware

```go
import "github.com/thescaffold/gox-packages-core/auth"

middleware := &auth.AuthMiddleware{Secret: os.Getenv("JWT_SECRET")}
// Attach to routes that require authentication
```

## CRUD Resource

```go
import "github.com/thescaffold/gox-packages-core/crud"

// CrudResource[MyEntity, CreateDto, UpdateDto] implements
// Index, Show, Create, Update, Destroy automatically
type MyController = crud.CrudResource[MyEntity, CreateDto, UpdateDto]
```

## Image Service

```go
import "github.com/thescaffold/gox-packages-core/image"

svc := &image.Service{}
svg := svc.New(image.Options{
    Width:   400,
    Height:  200,
    Text:    "Hello",
    Colors:  []string{"#4f46e5", "#818cf8"},
    Variant: image.VariantGradient,
})
```

## Development

```bash
go test ./...
go build ./...
```

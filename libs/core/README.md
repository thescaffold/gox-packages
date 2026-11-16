# gox-packages/core

Go library of cross-cutting concerns for Goose-based microservices.

## Overview

`core` provides the foundational building blocks shared across all gox services: authentication middleware, CRUD resource factories, HTTP client, event bus, context utilities, response helpers, security primitives, i18n, WebSocket, and common utilities.

## Sub-packages

| Package | Purpose |
|---------|---------|
| `auth` | JWT sign/verify, `AuthMiddleware` (Bearer), scope resolution |
| `context` | Request context extraction (`NTXContext`), `Middleware` |
| `crud` | Generic `CrudResource[E, C, U]` controller factory: bulk create/update, cursor pagination, tenant guard |
| `events` | In-process wildcard pub/sub `Bus`, `TrackerService`, `Default()` (the process's bus), `RegisterSubscriptions` (make an app's `Subscriptions` live) |
| `events/durable` | At-least-once subscriptions: bus events become goose queue jobs per subscription (retries, restart-safe, dead letters); feeds from the bus or the outbox |
| `events/dbevents` | `Publish(bus)`: rows written through Goose `sql.Entity` become `apps.db.<table>.after-insert\|update\|delete` events (opt-in, async, in order, after commit) |
| `filter` | Error/panic filter middleware |
| `http` | Retry-capable `Client` with HMAC signing (`Internal`) and plain (`External`) calls |
| `i18n` | YAML-based translation loader and render helper |
| `image` | SVG generator (solid, gradient, pixel variants) |
| `module` | `CoreModule` — wires all above services into a single goose module |
| `response` | Typed response helpers: `Success`, `NotFound`, `Unauthorized`, `Paginated`, etc. |
| `security` | `Hash`/`Compare` (bcrypt), `GenerateHmac` (SHA-256) |
| `utils` | UUID, time, money, random, type coercion, validation helpers |
| `ws` | WebSocket hub: namespaces and rooms, presence, a pub/sub `Adapter` for several nodes (`MemoryAdapter` in process), origin check, per-room (`AuthorizeJoin`) and per-message (`AuthorizeMessage`) authorization |

## Quick Start

```go
import "github.com/thescaffold/gox-packages/libs/core/module"

// In your AppModule.Imports():
module.New(module.CoreConfig{
    HMACKey: os.Getenv("HMAC_KEY"),
})
```

## Auth Middleware

```go
import "github.com/thescaffold/gox-packages/libs/core/auth"

middleware := &auth.AuthMiddleware{Secret: os.Getenv("JWT_SECRET")}
// Attach to routes that require authentication
```

## CRUD Resource

```go
import "github.com/thescaffold/gox-packages/libs/core/crud"

// CrudResource[MyEntity, CreateDto, UpdateDto] implements
// Index, Show, Create, Update, Destroy automatically
type MyController = crud.CrudResource[MyEntity, CreateDto, UpdateDto]
```

## Image Service

```go
import "github.com/thescaffold/gox-packages/libs/core/image"

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

## WebSocket hub (`ws`)

`ws.NewWithConfig` is the secure constructor; its defaults fail closed. It
refuses a config with no authenticator, no room authorizer, a `"*"` origin,
or client messages without a message authorizer.

```go
hub, err := ws.NewWithConfig(ws.Config{
    AuthenticateRequest: sessionFromCookie,            // or Authorize: token from an "auth"/"join" frame
    AuthorizeJoin: func(c *ws.Client, ns, room string) error {
        return members.Check(c.Subject(), ns, room)    // the tenant/membership check
    },
    AuthorizeMessage: func(c *ws.Client, m ws.Message) error { return nil },
    OnMessage:        chat.Ingest,                     // persist, sequence, then hub.Emit
    Presence:         true,
    Adapter:          ws.NewMemoryAdapter(),           // Redis/NATS later: same two methods
})
mux.Handle("/ws", http.HandlerFunc(hub.HandleHTTP))
hub.Emit("chat", "ws1/c42", "message.created", msg)  // every node
hub.Presence("chat", "ws1/c42")                       // who is here, every node
```

Client frames: `auth`, `join`, `leave`, `publish` (`event`, `payload`, `id`),
`presence` (`payload` = state). Server frames: `authenticated`, `joined`,
`left`, `event` (with `from` when relayed), `presence` (`sync`, `join`,
`leave`, `update`), `ack`, `error` (with the `id` it answers). Sockets close
when the token's `exp` passes, on an oversized frame (`MaxMessageBytes`,
64 KiB), or after two missed pings. `ws.New(authorize)` keeps the original
behaviour (any room, no client messages), but browser origins must now be
same-origin.

## CRUD: bulk, cursor, tenant guard

`CrudResource` also has `BulkCreate` (`POST /bulk`, `{"items":[…]}`),
`BulkUpdate` (`PATCH /bulk`, `{"items":[{"id":…,…}]}`) and `ListCursor`
(`GET /cursor?limit=&cursor=&order=`, keyset on `created_at, id`, meta
`{limit, order, has_more, next_cursor}`). Register them in the app's routes.
Batches are all-or-nothing, capped by `Config.BulkLimit` (100), and run
through the same hooks, validation, unique checks and workspace stamp as the
single-row handlers. An id outside the caller's scope fails the whole batch.

The tenant guard: a resource is scoped by `WorkspaceScoped`,
`WorkspaceScopeClause`, `OwnerScoped`, or an entity that calls goose
`RequireTenant` (the resource then binds it with `ForTenant(ctx.WorkspaceID)`
on every query). `Config.Global` marks a shared resource. With
`crud.RequireTenantScope(true)`, any other resource refuses every request;
`crud.UnscopedResources()` lists them, so a host test can keep the list empty.


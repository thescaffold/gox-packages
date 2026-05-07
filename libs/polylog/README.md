# gox-packages/polylog

Go library for structured analytics event tracking (identify, track, message).

## Overview

`polylog` wraps the `core/events` bus to provide a typed analytics interface mirroring the Segment/Amplitude event model. Register it as a goose module and inject `EventsService` to emit identify, track, and message events.

## Usage

```go
import "github.com/thescaffold/gox-packages-polylog"

polylog.Register(polylog.PolylogConfig{
    AppName: "my-service",
})
```

## API — `EventsService`

| Method | Signature | Description |
|--------|-----------|-------------|
| `Identify` | `(userId string, traits map)` | Identify a user with traits |
| `Track` | `(userId, event string, props map)` | Track a named event |
| `Message` | `(eventType string, props any)` | Publish a raw platform event |

## Development

```bash
go test ./...
go build ./...
```

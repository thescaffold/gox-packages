# gox-packages/flags

Go library for in-memory feature flag management.

## Overview

`flags` provides a `FlagService` with register/log/status/limit operations backed by an in-memory store. Register it as a goose module to make feature flags available across your service without external dependencies.

## Usage

```go
import "github.com/thescaffold/gox-packages-flags"

flags.Register(flags.FlagsConfig{
    Env: "production",
})
```

## API — `FlagService`

| Method | Description |
|--------|-------------|
| `Register(defs, env)` | Define one or more flags with their default values |
| `Log(name, opts)` | Record a flag evaluation event |
| `Status(names, opts)` | Return the current status of named flags |
| `Limit(name, opts)` | Apply a rate-limit check on a flag |

## Development

```bash
go test ./...
go build ./...
```

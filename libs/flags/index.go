// Package flags is a Go HTTP client for the scaffold flags server.
// It mirrors jsx-packages/libs/flags behavior: register/log/status/limit POSTs.
// Use FlagsModule.Register(cfg) to wire FlagService.
package flags

import "github.com/thescaffold/gox-packages/libs/flags/flag"

// Re-exports for single-import convenience.
type FlagService = flag.FlagService
type Flag = flag.Flag
type LogOpts = flag.LogOpts
type StatusOpts = flag.StatusOpts
type LimitOpts = flag.LimitOpts
type LimitResult = flag.LimitResult

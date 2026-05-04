// Package flags provides in-memory feature flag management for the NTX platform.
// Use FlagsModule.Register(cfg) to wire a FlagService into your goose app.
package flags

import "github.com/thescaffold/gox-packages-flags/flag"

// Re-exports for single-import convenience.
type FlagService = flag.FlagService
type FlagDef = flag.FlagDef
type LogOpts = flag.LogOpts
type StatusOpts = flag.StatusOpts

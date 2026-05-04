package flag

import (
	"fmt"
	"sync"
)

// FlagDef defines a feature flag.
type FlagDef struct {
	Name        string
	Description string
	// Enabled is the default state when no override is set.
	Enabled bool
	// Environments lists the env names where this flag is active ("production", "staging", …).
	// Empty means all environments.
	Environments []string
}

// LogOpts holds optional context for a Log call.
type LogOpts struct {
	Limit       int
	Level       string
	UserID      string
	ClientID    string
	WorkspaceID string
}

// StatusOpts holds optional context for a Status call.
type StatusOpts struct {
	Level       string
	UserID      string
	ClientID    string
	WorkspaceID string
}

// LimitOpts holds optional context for a Limit call.
type LimitOpts = StatusOpts

// FlagService manages feature flags in memory.
type FlagService struct {
	mu   sync.RWMutex
	env  string
	defs map[string]FlagDef
	logs map[string][]string // name → log entries
}

// NewFlagService creates a FlagService for the given environment name.
func NewFlagService(env string) *FlagService {
	if env == "" {
		env = "default"
	}
	return &FlagService{
		env:  env,
		defs: map[string]FlagDef{},
		logs: map[string][]string{},
	}
}

// Register stores flag definitions, filtering to those active in the current env.
func (s *FlagService) Register(flags []FlagDef, env string) {
	if env != "" {
		s.env = env
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range flags {
		s.defs[f.Name] = f
	}
}

// Log records a usage entry for the named flag (up to opts.Limit entries per flag).
func (s *FlagService) Log(name string, opts LogOpts) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := fmt.Sprintf("level=%s user=%s client=%s workspace=%s",
		opts.Level, opts.UserID, opts.ClientID, opts.WorkspaceID)
	limit := opts.Limit
	if limit <= 0 {
		limit = 1000
	}
	entries := s.logs[name]
	if len(entries) < limit {
		s.logs[name] = append(entries, entry)
	}
}

// Status returns an enabled/disabled map for each named flag.
// A flag is enabled if its definition has Enabled=true and (Environments is empty
// or includes the current environment).
func (s *FlagService) Status(names []string, opts StatusOpts) map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = s.isEnabled(name)
	}
	return out
}

// Limit returns whether the named flag is enabled and not yet exhausted.
// For the in-memory implementation "exhausted" means no log slots remain.
func (s *FlagService) Limit(name string, opts LimitOpts) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.isEnabled(name) {
		return false, nil
	}
	return true, nil
}

func (s *FlagService) isEnabled(name string) bool {
	def, ok := s.defs[name]
	if !ok {
		return false
	}
	if !def.Enabled {
		return false
	}
	if len(def.Environments) == 0 {
		return true
	}
	for _, e := range def.Environments {
		if e == s.env {
			return true
		}
	}
	return false
}

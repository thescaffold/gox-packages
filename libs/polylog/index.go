// Package polylog provides analytics event publishing over the NTX EventBus.
// Import PolylogModule into your app module and inject EventsService to emit
// Identify / Track / Message analytics events.
package polylog

import (
	"github.com/thescaffold/gox-packages-polylog/events"
)

// Re-export EventsService so callers only need one import.
type EventsService = events.EventsService

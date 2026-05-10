// Package polylog is a Go HTTP client for the scaffold polylog server.
// It mirrors jsx-packages/libs/polylog behavior: events are pushed to an
// in-process queue and a background flusher POSTs them to
// /apps/polylog/ingest/batch in 25-item chunks with exponential backoff.
package polylog

import "github.com/thescaffold/gox-packages/libs/polylog/events"

// Re-exports for single-import convenience.
type EventsService = events.EventsService
type Queue = events.Queue
type Flusher = events.Flusher
type Item = events.Item
type CategoryType = events.CategoryType
type EventEntityType = events.EventEntityType
type IdentifyOptions = events.IdentifyOptions
type TrackOptions = events.TrackOptions
type MessageOptions = events.MessageOptions

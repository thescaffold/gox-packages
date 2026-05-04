package events

import (
	"github.com/thescaffold/gox-packages-core/events"
	"github.com/thescaffold/gox-packages-core/utils"
)

// EventsService wraps the core TrackerService to provide analytics event publishing
// over the NTX EventBus. It mirrors the TS EventsService interface exactly.
type EventsService struct {
	tracker *events.TrackerService
}

// New creates an EventsService backed by the given TrackerService.
func New(tracker *events.TrackerService) *EventsService {
	return &EventsService{tracker: tracker}
}

// Identify publishes a user identity event.
func (s *EventsService) Identify(id string, attrs any) {
	s.tracker.Identify(id, toKV(attrs))
}

// Track publishes a named user action event.
func (s *EventsService) Track(id, name string, attrs any) {
	s.tracker.Track(id, name, toKV(attrs))
}

// Message publishes a typed event with arbitrary attributes.
func (s *EventsService) Message(eventType string, attrs any) {
	s.tracker.Message(eventType, attrs)
}

// toKV coerces any value to utils.KeyValue (map[string]any).
// nil and non-map types yield an empty map so callers never pass nil traits.
func toKV(v any) utils.KeyValue {
	if v == nil {
		return utils.KeyValue{}
	}
	if kv, ok := v.(utils.KeyValue); ok {
		return kv
	}
	if m, ok := v.(map[string]any); ok {
		return utils.KeyValue(m)
	}
	return utils.KeyValue{}
}

package events

// EventsService publishes analytics events to the polylog queue, where the
// Flusher batches them into POSTs to the scaffold server. Mirrors
// jsx-packages/libs/polylog/src/events/index.ts exactly:
//
//   - Identify pushes Item{Category: Event, Type: "user.identify",
//     Payload: {type:"identify", id, attributes, options}}
//   - Track pushes Item{Category: Event, Type: <eventType>,
//     Payload: {type:"track", id, attributes, options}}
//   - Message pushes Item{Category: Event, Type: <messageType>,
//     Payload: {...attributes}}  (spread, NOT wrapped)
type EventsService struct {
	queue *Queue
}

// New creates an EventsService backed by the given queue.
func New(queue *Queue) *EventsService {
	return &EventsService{queue: queue}
}

// Identify enqueues a user-identification event.
func (s *EventsService) Identify(id string, attributes any, options *IdentifyOptions) {
	s.queue.Push(Item{
		Category: CategoryEvent,
		Type:     "user.identify",
		Payload: map[string]any{
			"type":       "identify",
			"id":         id,
			"attributes": coerceAttrs(attributes),
			"options":    options,
		},
	})
}

// Track enqueues a named user-action event.
func (s *EventsService) Track(id, eventType string, attributes any, options *TrackOptions) {
	s.queue.Push(Item{
		Category: CategoryEvent,
		Type:     eventType,
		Payload: map[string]any{
			"type":       "track",
			"id":         id,
			"attributes": coerceAttrs(attributes),
			"options":    options,
		},
	})
}

// Message enqueues a typed event with a spread attribute payload.
// Note: unlike Identify/Track this does NOT wrap with type/id — it spreads
// attributes directly into the payload, mirroring jsx-polylog message().
func (s *EventsService) Message(messageType string, attributes any, options *MessageOptions) {
	_ = options // jsx-polylog message() drops options; preserved here for parity
	s.queue.Push(Item{
		Category: CategoryEvent,
		Type:     messageType,
		Payload:  coerceAttrs(attributes),
	})
}

// coerceAttrs returns a usable map for the payload. nil and unrecognized types
// yield an empty map so a caller never enqueues nil traits.
func coerceAttrs(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

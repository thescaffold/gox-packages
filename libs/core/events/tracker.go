package events

import (
	"strings"

	"github.com/thescaffold/gox-packages/libs/core/utils"
)

// TrackerService mirrors the TS TrackerService, publishing typed analytics events
// via the shared Bus.
type TrackerService struct {
	bus *Bus
}

// NewTrackerService creates a TrackerService backed by bus.
func NewTrackerService(bus *Bus) *TrackerService {
	return &TrackerService{bus: bus}
}

// Identify emits an identify event for a user.
func (t *TrackerService) Identify(userID string, traits utils.KeyValue) {
	payload := utils.KeyValue{"userId": userID}
	for k, v := range traits {
		payload[k] = v
	}
	t.bus.Publish("apps.common..user.identify", payload)
}

// Track emits a named analytics event for a user.
func (t *TrackerService) Track(userID, event string, properties utils.KeyValue) {
	payload := utils.KeyValue{"userId": userID}
	for k, v := range properties {
		payload[k] = v
	}
	t.bus.Publish("apps.common..user."+event, payload)
}

// Page emits a page-view event for a user.
func (t *TrackerService) Page(userID, category, name string, properties utils.KeyValue) {
	payload := utils.KeyValue{
		"userId":   userID,
		"category": category,
		"name":     name,
	}
	for k, v := range properties {
		payload[k] = v
	}
	t.bus.Publish("apps.common..user.page", payload)
}

// Message publishes a raw platform event — the primary inter-service signalling method.
// eventType is lowercased before publishing, matching the TS behaviour.
func (t *TrackerService) Message(eventType string, properties any) {
	t.bus.Publish(strings.ToLower(eventType), properties)
}

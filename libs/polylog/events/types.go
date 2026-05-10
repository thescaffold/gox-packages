package events

// CategoryType mirrors jsx-polylog/common/utils/values.ts CategoryType.
type CategoryType string

const (
	CategoryForm    CategoryType = "form"
	CategoryFile    CategoryType = "file"
	CategoryWebhook CategoryType = "webhook"
	CategoryJob     CategoryType = "job"
	CategoryEvent   CategoryType = "event"
	CategoryLog     CategoryType = "log"
	CategoryMotion  CategoryType = "motion"
	CategoryMessage CategoryType = "message"
	CategoryOthers  CategoryType = "others"
)

// EventEntityType mirrors jsx-polylog EventEntityType.
type EventEntityType string

const (
	EventEntitySource  EventEntityType = "source"
	EventEntityChannel EventEntityType = "channel"
)

// LogType mirrors jsx-polylog LogType.
type LogType string

const (
	LogInfo  LogType = "info"
	LogWarn  LogType = "warn"
	LogError LogType = "error"
)

// Item is the queue envelope sent to /apps/polylog/ingest/batch.
// Mirrors jsx-polylog Item interface.
type Item struct {
	Category CategoryType `json:"category"`
	Payload  any          `json:"payload"`

	EntityID   string          `json:"entityId,omitempty"`
	EntityName EventEntityType `json:"entityName,omitempty"`

	Reference string `json:"reference,omitempty"`
	Version   string `json:"version,omitempty"`

	SourceID string `json:"sourceId,omitempty"`
	Source   string `json:"source,omitempty"`
	Type     string `json:"type,omitempty"`
}

// Options stubs (jsx-polylog defines them as empty interfaces; preserved for API parity).
type IdentifyOptions struct{}
type TrackOptions struct{}
type MessageOptions struct{}

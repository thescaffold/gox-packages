package utils

// ItemDto mirrors TS ItemDto (dtos.util.ts) — the validated request body shape
// for ingest endpoints. Validation tags follow Go's convention; downstream
// validators can enforce the constraints below.
type ItemDto struct {
	EntityID   string          `json:"entityId" validate:"uuid"`
	EntityName EventEntityType `json:"entityName" validate:"oneof=source channel"`
	Category   CategoryType    `json:"category" validate:"required"`
	SourceID   string          `json:"sourceId,omitempty" validate:"omitempty,uuid"`
	Source     string          `json:"source,omitempty"`
	Type       string          `json:"type,omitempty"`
	Reference  string          `json:"reference,omitempty"`
	Version    string          `json:"version,omitempty"`
	Payload    any             `json:"payload" validate:"required"`
}

// ItemsDto wraps a slice of ItemDto for batch endpoints. Mirrors TS ItemsDto.
type ItemsDto struct {
	Items []ItemDto `json:"items,omitempty"`
}

// SourceOrChannelItem mirrors TS SourceOrChannelItem — a slimmer payload for
// per-source / per-channel ingest where the entity is implied by the route.
type SourceOrChannelItem struct {
	Type      string       `json:"type,omitempty"`
	Reference string       `json:"reference,omitempty"`
	Version   string       `json:"version,omitempty"`
	Category  CategoryType `json:"category"`
	Payload   any          `json:"payload"`
}

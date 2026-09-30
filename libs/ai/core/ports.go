package core

import (
	"context"
	"time"
)

// CredentialStore resolves provider credentials by reference. Origine binds it
// to envelope-encrypted secrets; tests bind an in-memory map. Get returns
// ErrNotFound for an unknown ref. A returned secret must never be logged or put
// in a prompt.
type CredentialStore interface {
	Get(ctx context.Context, ref string) (map[string]string, error)
}

// UsageEvent is one metered model call.
type UsageEvent struct {
	// ID is unique per call and stable across retries of the same call, so a
	// sink can be idempotent: recording the same ID twice must not double-charge.
	ID          string
	Provider    string
	Model       string
	Usage       Usage
	Cost        MicroUSD
	WorkspaceID string
	RunID       string
	StepID      string
	At          time.Time
}

// UsageSink receives one UsageEvent per model call (Origine: the ledger).
// Record must be idempotent on UsageEvent.ID.
type UsageSink interface {
	Record(ctx context.Context, e UsageEvent) error
}

// ResponseCache is an optional cache of deterministic responses, keyed on the
// canonicalised request. Get returns (nil, nil) on a miss.
type ResponseCache interface {
	Get(ctx context.Context, key string) (*ChatResponse, error)
	Set(ctx context.Context, key string, r *ChatResponse, ttl time.Duration) error
}

// MessageStore is the append-only history of a run. There is deliberately no
// update or delete: edited history is rejected by newer models, and context is
// reduced by appending a summary. List returns messages in append order.
type MessageStore interface {
	Append(ctx context.Context, runID string, m Message) error
	List(ctx context.Context, runID string) ([]Message, error)
}

// RunStatus is where a run is in its life.
type RunStatus string

const (
	RunPending      RunStatus = "pending"
	RunRunning      RunStatus = "running"
	RunAwaitingTool RunStatus = "awaiting_tool"
	// RunAwaitingApproval: a guarded tool call is waiting for a person.
	RunAwaitingApproval RunStatus = "awaiting_approval"
	// RunBlockedCredits: the ledger hold cannot cover the next step; resumes on top-up.
	RunBlockedCredits RunStatus = "blocked_credits"
	RunSucceeded      RunStatus = "succeeded"
	RunFailed         RunStatus = "failed"
	RunCancelled      RunStatus = "cancelled"
)

// Terminal reports whether no further step will ever run.
func (s RunStatus) Terminal() bool {
	return s == RunSucceeded || s == RunFailed || s == RunCancelled
}

// StepRecord is one step: a model call plus any tool dispatch that followed.
type StepRecord struct {
	ID         string
	RunID      string
	Index      int
	Message    Message
	StopReason StopReason
	Usage      Usage
	Cost       MicroUSD
	Model      string
	StartedAt  time.Time
	EndedAt    time.Time
}

// ToolCallRecord is one tool call made within a step.
type ToolCallRecord struct {
	ID       string
	RunID    string
	StepID   string
	Name     string
	Input    []byte
	Output   Content
	IsError  bool
	Duration time.Duration
	At       time.Time
}

// AgentStepStore persists steps and the run's status. Save is an upsert on
// StepRecord.ID so a resumed run can re-save a step it had already recorded.
type AgentStepStore interface {
	Save(ctx context.Context, s StepRecord) error
	List(ctx context.Context, runID string) ([]StepRecord, error)
	SetStatus(ctx context.Context, runID string, status RunStatus, detail string) error
}

// AgentToolCallStore persists tool calls. Save is an upsert on ToolCallRecord.ID.
type AgentToolCallStore interface {
	Save(ctx context.Context, c ToolCallRecord) error
	List(ctx context.Context, runID string) ([]ToolCallRecord, error)
}

// PromptVersion is one immutable revision of a template.
type PromptVersion struct {
	ID         string
	TemplateID string
	Version    int
	// Body holds {{var}} / {{#if}} placeholders.
	Body string
	// Variables are the declared placeholders, for validation and UI.
	Variables []string
	CreatedAt time.Time
}

// PromptTemplate is a named template; ActiveVersionID points at the live version.
type PromptTemplate struct {
	ID              string
	Key             string // stable lookup key, e.g. "ai.reviewer.system"
	Description     string
	ActiveVersionID string
	// WorkspaceID is set for a tenant override, empty for the platform default.
	WorkspaceID string
}

// PromptStore resolves a template by key, preferring the workspace's override
// over the platform default. It returns ErrNotFound when neither exists.
type PromptStore interface {
	GetByKey(ctx context.Context, key, workspaceID string) (*PromptTemplate, error)
}

// PromptVersionStore reads template versions.
type PromptVersionStore interface {
	Get(ctx context.Context, id string) (*PromptVersion, error)
	GetActive(ctx context.Context, templateID string) (*PromptVersion, error)
}

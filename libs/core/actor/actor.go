// Package actor implements the actor reference convention Origine's TRD §5
// defines: "An actor reference is (actor_type, actor_id) with actor_type ∈
// {user, ai, system}; for ai, actor_id is the role key." It's cross-cutting
// — audit.Log, tasks.TaskEvent, agents.Run and evidence.Evidence all carry
// one — so it lives here rather than duplicated per consumer. Hoisted from
// gox-apps/libs/tasks/pkg/actor (Origine, private) once a second consumer
// (audit.Log, PLAN M1-04) needed the same shape; tasks now re-exports this
// package's types instead of defining its own.
package actor

// Type is one of the three actor kinds.
type Type string

const (
	TypeUser   Type = "user"
	TypeAI     Type = "ai"
	TypeSystem Type = "system"
)

// Actor is stored as jsonb (or, where a column pair is simpler than a
// nested object — see audit.Log — as two plain columns) on anything TRD
// calls "an actor".
type Actor struct {
	Type Type   `json:"type"`
	Id   string `json:"id"`
}

// Valid reports whether a is a well-formed actor reference — a real Type
// and a non-empty Id (for `ai`, the Id is the role key, still required).
func (a Actor) Valid() bool {
	switch a.Type {
	case TypeUser, TypeAI, TypeSystem:
		return a.Id != ""
	default:
		return false
	}
}

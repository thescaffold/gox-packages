// Package core is the provider-neutral vocabulary of the AI runtime: content
// blocks, Message, ToolDef, Usage and cost, typed errors, and the ports the
// agent loop persists and meters through (TRD §6.2, PLAN M1-25).
//
// It depends on the standard library only, so a driver, the agent loop, an
// MCP bridge and a host app can all import it without pulling each other in.
// Anything Anthropic- or OpenAI-specific belongs in a driver, not here.
//
// # Ports
//
// The hosting application binds these; the runtime never reaches for a
// database, a vault or a ledger itself:
//
//   - CredentialStore: provider secrets by reference (Origine: envelope-encrypted).
//   - UsageSink: one UsageEvent per model call (Origine: the ledger).
//   - ResponseCache: optional deterministic-response cache.
//   - MessageStore: the append-only conversation history of a run.
//   - AgentStepStore and AgentToolCallStore: what each step and tool call did.
//   - CancelToken: cooperative cancellation a long loop polls between steps.
//   - PromptStore and PromptVersionStore: versioned templates with tenant overrides.
//
// # History is append-only
//
// Newer models bind thinking blocks to the turn that produced them and reject
// edited history, so MessageStore has no update or delete. Shrinking context is
// done by adding a summary message, never by rewriting one.
package core

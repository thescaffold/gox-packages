# ai

The provider-neutral AI runtime for Origine (TRD §6.2). Go, one module, packages:

| Package | What it is |
| --- | --- |
| `core` | Content blocks, `Message`, `ToolDef`, `Usage` and integer micro-USD cost, typed errors, JSON-Schema input validation, and the ports the host binds (`CredentialStore`, `UsageSink`, `ResponseCache`, `MessageStore`, `AgentStepStore`, `AgentToolCallStore`, `CancelToken`, `PromptStore`). Standard library only. |
| `llm` | `ProviderDriver`, request validation every driver shares, the stream event vocabulary and the `Accumulator` that turns events into a `ChatResponse`. |
| `llm/anthropic` | The Anthropic Messages driver: always streams, adaptive thinking, effort, strict and eager tools (eager input is validated client-side), refusals, cache hints, typed errors. |
| `policy` | Tiers → model chains, retention and spend constraints, a per-provider circuit breaker and a failover `Router`. |
| `prompt` | Versioned templates with tenant overrides, override validation, and `WrapUntrusted`. |
| `agent` | The turn loop: tool dispatch, budgets, cancellation, loop detection, pause and resume. |
| `testing` | A scripted fake driver, in-memory ports, and `RunDriverContract`, which every driver must pass. |

## Rules the code enforces

- **History is append-only.** There is no update or delete on `MessageStore`; thinking blocks and their signatures are replayed exactly as produced.
- **Nothing runs on unchecked input.** Every tool call is validated against its schema; a cut-off (`max_tokens`), refused or over-long turn never executes a call.
- **Money is integer micro-USD.** Cost rounds up once over the sum, so a call is never under-charged.
- **A zero-retention tenant never resolves Fable 5.1**, and frontier models stay off unless the spend policy allows them.
- **A worker can die at any write and the run resumes**: tool calls are recorded as started *before* they run, completed calls are reused, and a call whose outcome was lost is re-run only if the tool says that is safe (`ResumeSafer`).

## Writing a driver

Implement `llm.ProviderDriver`, call `llm.ValidateRequest` first, build the response with `llm.Accumulator`, and pass the contract:

```go
func TestMyDriver(t *testing.T) { aitesting.RunDriverContract(t, myHarness{}) }
```

The harness turns a provider-neutral `Scenario` (reply, provider error, stall) into whatever the driver needs, typically a fixture server. `llm/anthropic/harness_test.go` is the worked example.

## Not done yet

- The Anthropic live smoke test (`TestLiveSmoke`) runs only when `ANTHROPIC_API_KEY` is set; the wire fixtures are written from the documented event format, not recorded from the live API.
- Server-side model fallbacks and the advisory `TaskBudget` are not sent to the API: their wire shape is not verified. Fallback is done client-side by `policy.Router`.
- Model capability rules in `llm/anthropic/catalog.go` (context window, output limit, which models accept effort) are conservative defaults to re-check against the API reference.

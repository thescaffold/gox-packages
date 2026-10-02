# design

The system's design as a graph, and the checks that keep it consistent. The graph
is **compiled from the spec** (`spec.Compile` in `libs/spec`), never edited on its
own: a change to the design is a change to the spec (TRD §6.5).

| Nodes | Edges |
| --- | --- |
| `actor` `capability` `service` `entity` `datastore` `interface` `integration` `job` `environment` | `uses` `owns` `calls` `publishes` `deploys_to` |

- Every node's `id` is the id of the spec item it came from, so it survives
  revisions and renames. `inferred` marks what the Architect filled in; each edge says
  which item it was written in (`source`).
- `Graph.Validate()` is the referential-integrity check: ids are unique, every edge
  and every reference field points at something that is there, edges join only the
  kinds they may (`Allowed`), no edge is repeated, and key lists hold setting
  names, never values.
- `Schema()` is the JSON Schema of the graph, built from the Go types so it cannot
  drift. `Parse` reads JSON strictly (unknown fields are an error) and validates.
  `JSON()` is stable: edges come out sorted.
- Standard library only.

## Impact

`Impact(before, after)` says what a change to the design makes stale (TRD §6.5):

- **Affected**: what changed, plus whatever depends on it. Something that uses or
  calls a thing depends on it; an owner depends on what it owns; an environment
  depends on what is deployed to it. People and environments are ends of the line.
  Each says in plain words why. Descriptions, code ownership and `inferred` marks are
  not design changes, so a wording-only edit has no impact.
- **Tests**: the criteria of affected capabilities (`rerun`), with new, changed and
  removed ones marked.
- **Code**: the paths that own affected parts, before or after; `Unbuilt` names
  affected parts with no code yet.
- **Artifacts**: the generated files to make again (`design.json`, `design.mmd`,
  `erd.mmd`, `openapi.yaml`, `prototype`, `PRD.md`, `TRD.md`).
- **Work**: the plan, in plain words: build, remove, update, move, disconnect,
  connect, deploy, re-check. A build plans from this.

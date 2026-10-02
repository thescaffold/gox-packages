# spec

Reads, prints and normalises **OSpec**, the structured-Markdown language a
system's specification is written in (Origine TRD §6.18 and Appendix E). Standard
library only, so it can also be compiled to WebAssembly.

```go
doc, diags := spec.Parse(text)        // never fails; diags are plain-language notes
fixes := spec.Canonicalize(doc, prev) // ids, duplicate ids, renamed references
out := spec.Print(doc)                // canonical text
```

## What it guarantees

- **Nothing is dropped.** Text the parser does not understand is kept as a `Raw`
  node, printed back as written, and reported as a `Diagnostic` with a line,
  column and a sentence a non-engineer can follow. Fenced code, HTML comments and
  free text in note sections are kept too. (Trailing spaces and carriage returns
  are not content and are not kept; an unclosed comment or code block is closed at
  the end of the file, with a diagnostic.)
- **Printing is stable.** `Print(Parse(Print(Parse(x)))) == Print(Parse(x))` for
  every input, checked by a fuzz test (`FuzzParsePrint`) that also checks every
  letter and digit of the input survives printing and that `Canonicalize` is a
  fixed point with unique ids.
- **Ids without burden.** `Canonicalize` adds `{#id}` to every item and to every
  `must:`, `done when:` and `not now:` entry: a slug of the title (`recurring-orders`),
  `q-`, `d-`, `a-` and a few words for questions, decisions and assumptions, and
  `c-` plus four hex digits for anonymous ones. A repeated id gives the later copy a
  new one. Given the previous revision, a renamed item keeps its id and every
  `[[Old title]]` becomes `[[New title]]`.

## Editing a spec

```go
patch, _ := spec.ParsePatch(jsonBytes)    // {"base":17,"ops":[{"op":"add",...}]}
patch.Check(true)                          // every step has a target and a one-line reason
next, err := spec.Apply(doc, patch)        // atomic: all steps or none; error names the step
undo, _ := spec.Invert(doc, patch)         // apply(next, undo) prints exactly as doc
changes := spec.Diff(doc, next)            // by id; spec.Semantic(changes) drops wording-only ones
merged, conflicts := spec.Merge3(base, mine, theirs)
diags := spec.Validate(merged)             // spec.HasErrors, spec.HasSecret
```

Operations: `add`, `set`, `text`, `move`, `remove`, `rename`, `split`, `merge`,
`answer`. Each is addressed by id. `Invert` also uses `replace`, `restore`,
`prune` and `section` to put things back exactly. A patch that leaves a reference
pointing nowhere still applies (that is a warning, not an error); one that would
leave a repeated id does not.

`Merge3` keeps *mine* wherever both sides changed the same thing differently and
says so in the returned conflicts; order within a section or list is mine's.

## Layout

| File | What it does |
| --- | --- |
| `lex.go` | classifies each line on its own (heading, bullet, `key: value`, ...) |
| `parse.go` | builds the tree and the diagnostics |
| `print.go` | canonical text |
| `canon.go` | `Canonicalize` |
| `refs.go` | `[[references]]`, `Addressables`, `Resolve` |
| `validate.go`, `secrets.go` | `Validate` (errors block a commit; warnings do not), `ScanSecrets` |
| `patch.go`, `apply.go`, `invert.go`, `clone.go` | `SpecPatch` (JSON, App. E.5), atomic `Apply`, exact `Invert` |
| `diff.go` | `Diff`: changes by id, each semantic or prose-only |
| `merge.go` | `Merge3`: item-level three-way merge with explicit conflicts |
| `compile.go` | `Compile`: spec → design graph |
| `impact.go` | `Impact(before, after, ownership)`: compile both, compare the designs |
| `outline.go` | `Outline`: the compact view given to models |
| `testdata/` | `valid/` (canonical files, a fixed point), `messy/` (hand-typed input and its reviewed `.golden`), `invalid/` (and the expected `.diag`) |

`Compile(doc)` turns a spec into a `design.Graph` (`libs/design`): users become actors, features capabilities, data entities (with typed fields), screens interfaces, parts services, jobs or interfaces, integrations and environments; "for:", "uses:", "needs:", "shows:", "does:", "owns:" and "talks to:" become edges, every integration's keys are declared in every environment, and every deployable goes to every environment. Items marked `inferred: yes` (or ending "(inferred by Origine)") and what comes from them are marked inferred. A relation that cannot be drawn is left out with a warning, and a value where a setting name or one of a few allowed words belongs never reaches the graph.

Regenerate goldens after a deliberate change with
`go test -update -run 'Golden|Diagnostics' .` and review the diff.

## In the browser (WebAssembly)

`wasm/` builds the same Go code for the browser: `GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o spec.wasm ./wasm`
(with `wasm_exec.js` from `$(go env GOROOT)/lib/wasm`). It exposes one function, `ospecAnalyze(text)`, which returns the JSON of
`spec.Analyze`: the canonical text, the plain-language notes (parse, validate, secrets), the outline and the title. The server calls the very
same `Analyze`, so there is one parser, never two.

**Decision (M2-01d): the editor runs the WebAssembly build; the server stays the authority.** Notes appear as the person types, with no round trip,
and the commit path still parses and validates on the server with the same code, so what the editor showed is what the commit decides. If the
WebAssembly file cannot be loaded (blocked, offline, old browser) the editor falls back to asking the server, which gives the same answers.

| Measured | Result |
|---|---|
| File size | 3.4 MB raw, 953 KB gzip -9, **719 KB brotli -q 11** |
| Load, Chromium (localhost, cache cold), compile + instantiate | 7 to 12 ms; the download is the cost: 719 KB is about 0.6 s at 10 Mbps, once, then cached |
| First call | about 1 to 10 ms |
| Per call, a 2.7 KB document (Chromium) | median 0.5 ms, p95 under 1.3 ms |
| Per call, 531 KB of text (Node) | 180 ms; typical specs are 5 to 60 KB |

Serve it with brotli, `Cache-Control: immutable` and a content-hashed file name, and load it when the Editor mode opens, not at page load.
TinyGo was not tried: it would be a second compiler with its own gaps in `reflect` and `encoding/json`, which is the "second parser" risk the
differential test exists to rule out, and 719 KB is acceptable.

**Differential test** (`TestWasmMatchesNative`, skipped under `-short` or without Node): builds the browser file, runs the example documents plus 600 seeded
random mutations of them (cut, repeated, swapped chunks, stray bytes, odd characters) and a 531 KB document through it under Node, and
requires every answer to equal the native one byte for byte. 616 of 616 are identical.

Found while measuring (fixed): resolving `[[references]]` re-read every title once per reference, and finding a free `-2`, `-3` id restarted from 2 each time, so a long spec
or a spec with many same-named items grew quadratically (a 160 KB document took 41 ms, a 1 MB one took 1.1 s). The title index is now kept on the
document and verified on each use, `Validate` and `Compile` resolve against one snapshot, and the id search resumes where it stopped. 160 KB takes 12 ms; 4 MB, 410 ms.

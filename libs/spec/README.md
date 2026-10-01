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

## Layout

| File | What it does |
| --- | --- |
| `lex.go` | classifies each line on its own (heading, bullet, `key: value`, ...) |
| `parse.go` | builds the tree and the diagnostics |
| `print.go` | canonical text |
| `canon.go` | `Canonicalize` |
| `refs.go` | `[[references]]`, `Addressables`, `Resolve` |
| `testdata/` | `valid/` (canonical files, a fixed point), `messy/` (hand-typed input and its reviewed `.golden`), `invalid/` (and the expected `.diag`) |

Not here yet (M2-01b and M2-01c): `Validate`, `Diff`, `SpecPatch` `Apply`/`Invert`,
`Merge3`, and `Compile` to a design graph.

Regenerate goldens after a deliberate change with
`go test -update -run 'Golden|Diagnostics' .` and review the diff.

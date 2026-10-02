# render

Markdown → one self-contained HTML page, a Word document and a PDF (TRD §6.10, PLAN M2-10).

```go
html, _ := render.HTML(markdown, render.Options{Title: "PRD", Footer: "Made from revision 7"})
docx, _ := render.DOCX(markdown, render.Options{})
pdf, err := render.PDF(ctx, markdown, render.Options{}) // needs Chromium; render.ErrNoBrowser without one
```

- **HTML** has its own styles (light, dark, print) and a policy that loads nothing: no scripts, no remote images (an image that
  would be fetched becomes its description; `data:` images stay), no fonts. Raw HTML in the Markdown is dropped.
- **DOCX** is written here (no pandoc): headings, paragraphs, bold/italic/strike/code, links, nested bullet and numbered lists
  (each numbered list restarts at 1), task lists, tables with a repeating header row, quotes, code blocks, rules. Plain OOXML, no
  macros; the same text gives the same bytes. Checked by reading the package back and by macOS `textutil` opening it.
- **PDF** is printed from the HTML by headless Chromium (found on `PATH`, or `Options.Chrome`, or `RENDER_CHROME`), in a throwaway
  profile with name resolution switched off, bounded by the context (60 s if it has no deadline). Tested with
  `RENDER_CHROME=.../chrome-headless-shell go test ./...`.
- The largest document we have (the 300 KB TRD) renders in 5 ms (HTML), 16 ms (DOCX) and 0.8 s (PDF).

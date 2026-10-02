// Package render turns Markdown documents (the PRD, the TRD, the decision log, the spec
// preview) into the files people send around: one self-contained HTML page, a PDF and a
// Word document (TRD §6.10, PLAN M2-10).
//
//   - HTML needs nothing but this package. The page has its own styles and a policy that
//     forbids loading anything from the network, so it reads the same offline.
//   - DOCX is written here too (a small, plain subset of Word: headings, paragraphs, bold,
//     italic, code, links, lists, tables, quotes), so there is no tool to install.
//   - PDF is printed from the HTML by a headless Chromium, which has to be on the machine
//     (or named in Options.Chrome, or the RENDER_CHROME variable). Without one, PDF returns
//     ErrNoBrowser and the other two still work.
//
// Raw HTML inside the Markdown is dropped, never passed through: a document written by a
// model or a person cannot put a script or a tracking image into the page.
package render

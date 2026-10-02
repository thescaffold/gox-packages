package render

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// Options shapes a rendering.
type Options struct {
	// Title is the document's title (the page title, the Word title). When empty, the first heading is used.
	Title string
	// Lang is the language of the text, "en" when empty.
	Lang string
	// Footer is a line printed under the document (who made it, from which revision).
	Footer string
	// Chrome is the path of a Chromium or Chrome binary for PDF; empty looks for one.
	Chrome string
}

// ErrEmpty: there was nothing to render.
var ErrEmpty = errors.New("render: the document is empty")

var md = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.TaskList),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	// no WithUnsafe: raw HTML in the source is omitted, not passed through
	goldmark.WithRendererOptions(gmhtml.WithXHTML(), renderer.WithNodeRenderers(util.Prioritized(imageRenderer{}, 100))),
)

// imageRenderer keeps an inline image only when it is data in the document itself
// (a data: URI); an image that would be fetched from somewhere becomes its description,
// so a document cannot make the reader's browser call out.
type imageRenderer struct{}

func (imageRenderer) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(ast.KindImage, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		img := n.(*ast.Image)
		if !entering {
			return ast.WalkContinue, nil
		}
		alt := plain(img, src)
		if d := string(img.Destination); strings.HasPrefix(strings.ToLower(d), "data:image/") {
			_, _ = w.WriteString(`<img src="` + html.EscapeString(d) + `" alt="` + html.EscapeString(alt) + `">`)
		} else {
			_, _ = w.WriteString(`<span class="image">[` + html.EscapeString(alt) + `]</span>`)
		}
		return ast.WalkSkipChildren, nil
	})
}

// HTML renders Markdown as one self-contained HTML page.
func HTML(markdown string, o Options) ([]byte, error) {
	if strings.TrimSpace(markdown) == "" {
		return nil, ErrEmpty
	}
	var body bytes.Buffer
	if err := md.Convert([]byte(markdown), &body); err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	title := o.Title
	if title == "" {
		title = firstHeading(markdown)
	}
	lang := o.Lang
	if lang == "" {
		lang = "en"
	}
	var out bytes.Buffer
	out.WriteString("<!doctype html>\n<html lang=\"" + html.EscapeString(lang) + "\">\n<head>\n<meta charset=\"utf-8\">\n")
	out.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	// nothing may be loaded: no scripts, no remote images, no fonts
	out.WriteString("<meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; style-src 'unsafe-inline'; img-src data:\">\n")
	out.WriteString("<title>" + html.EscapeString(title) + "</title>\n<style>" + css + "</style>\n</head>\n<body>\n<main>\n")
	out.Write(body.Bytes())
	out.WriteString("</main>\n")
	if o.Footer != "" {
		out.WriteString("<footer>" + html.EscapeString(o.Footer) + "</footer>\n")
	}
	out.WriteString("</body>\n</html>\n")
	return out.Bytes(), nil
}

func firstHeading(markdown string) string {
	for _, line := range strings.Split(markdown, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			return strings.TrimSpace(strings.TrimLeft(t, "#"))
		}
	}
	return "Document"
}

const css = `
:root{color-scheme:light dark}
body{font:16px/1.6 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;margin:0;color:#1f2328;background:#fff}
main{max-width:46rem;margin:0 auto;padding:2rem 1.25rem}
h1,h2,h3,h4{line-height:1.25;margin:1.6em 0 .5em}
h1{font-size:2rem;border-bottom:1px solid #d0d7de;padding-bottom:.3em}
h2{font-size:1.5rem;border-bottom:1px solid #d0d7de;padding-bottom:.3em}
a{color:#0969da}
code,pre{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:.9em}
code{background:#f6f8fa;padding:.1em .3em;border-radius:4px}
pre{background:#f6f8fa;padding:1em;border-radius:6px;overflow:auto}
pre code{background:none;padding:0}
blockquote{margin:1em 0;padding:0 1em;color:#59636e;border-left:.25em solid #d0d7de}
table{border-collapse:collapse;margin:1em 0;display:block;overflow:auto}
th,td{border:1px solid #d0d7de;padding:.4em .8em;text-align:left}
th{background:#f6f8fa}
hr{border:0;border-top:1px solid #d0d7de;margin:2em 0}
li:has(>input[type=checkbox]){list-style:none;margin-left:-1.3em}
footer{max-width:46rem;margin:0 auto;padding:1rem 1.25rem 2rem;color:#59636e;font-size:.85rem;border-top:1px solid #d0d7de}
@media (prefers-color-scheme:dark){body{background:#0d1117;color:#e6edf3}h1,h2,hr,footer,th,td,blockquote{border-color:#30363d}code,pre,th{background:#161b22}a{color:#4493f8}blockquote{color:#9198a1}}
@media print{body{background:#fff;color:#000}main{max-width:none;padding:0}a{color:#000}pre{white-space:pre-wrap}h1,h2,h3{break-after:avoid}table,pre,blockquote{break-inside:avoid}@page{margin:18mm}}
`

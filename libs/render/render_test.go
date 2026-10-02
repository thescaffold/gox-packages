package render_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thescaffold/gox-packages/libs/render"
)

func reference(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/reference.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHTMLIsOnePageThatLoadsNothing(t *testing.T) {
	b, err := render.HTML(reference(t), render.Options{Footer: "Made from revision 7"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"<title>Grocery Restocking — Product Requirements</title>", "<h2 id=\"features\">Features</h2>", "<table>", "<blockquote>", "<pre><code class=\"language-go\">", "<del>Decimals</del>", `type="checkbox"`, "Made from revision 7", "default-src 'none'"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	// raw HTML in the source never reaches the page
	for _, bad := range []string{"<script", "alert(1)", "<b>raw html</b>"} {
		if strings.Contains(s, bad) {
			t.Errorf("the page contains %q", bad)
		}
	}
	// nothing is loaded from outside, except links a person may click
	for _, bad := range []string{"<link", "src=\"http", "@import", "url(http"} {
		if strings.Contains(s, bad) {
			t.Errorf("the page loads something: %q", bad)
		}
	}
	// same text, same bytes
	again, _ := render.HTML(reference(t), render.Options{Footer: "Made from revision 7"})
	if !bytes.Equal(b, again) {
		t.Error("not deterministic")
	}
}

func TestEmptyAndTitles(t *testing.T) {
	for _, f := range []func(string, render.Options) ([]byte, error){render.HTML, render.DOCX} {
		if _, err := f("  \n", render.Options{}); !errors.Is(err, render.ErrEmpty) {
			t.Errorf("empty: %v", err)
		}
	}
	b, _ := render.HTML("just words", render.Options{Title: "A <title>"})
	if !strings.Contains(string(b), "<title>A &lt;title&gt;</title>") {
		t.Errorf("title not escaped: %s", b)
	}
}

func unzip(t *testing.T, b []byte) map[string]string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range z.File {
		r, _ := f.Open()
		data, _ := io.ReadAll(r)
		r.Close()
		out[f.Name] = string(data)
	}
	return out
}

func TestDOCXIsAWellFormedPackageWithWhatTheDocumentSays(t *testing.T) {
	b, err := render.DOCX(reference(t), render.Options{Footer: "Made from revision 7"})
	if err != nil {
		t.Fatal(err)
	}
	parts := unzip(t, b)
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "docProps/core.xml", "word/document.xml", "word/styles.xml", "word/numbering.xml", "word/_rels/document.xml.rels"} {
		body, ok := parts[name]
		if !ok {
			t.Fatalf("missing part %s", name)
		}
		// every part is well-formed XML
		d := xml.NewDecoder(strings.NewReader(body))
		for {
			if _, err := d.Token(); err != nil {
				if err != io.EOF {
					t.Fatalf("%s: %v", name, err)
				}
				break
			}
		}
	}
	doc := parts["word/document.xml"]
	for _, want := range []string{
		`<w:pStyle w:val="Heading1"/>`, `<w:pStyle w:val="Heading3"/>`, "Grocery Restocking — Product Requirements",
		`<w:pStyle w:val="Quote"/>`, `<w:tbl>`, `<w:tblHeader/>`, "one of waiting, paid, delivered",
		`<w:pStyle w:val="Code"/>`, "type Money int64 // kobo", "<w:strike/>", `Consolas`, "☑ ", "☐ ", "Made from revision 7",
		`<w:hyperlink r:id="rId11"`, `<w:b/>`, `<w:i/>`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("document.xml lacks %q", want)
		}
	}
	for _, bad := range []string{"alert(1)", "<script", "raw html</w:t>"} {
		if strings.Contains(doc, bad) && bad != "raw html</w:t>" {
			t.Errorf("document.xml contains %q", bad)
		}
	}
	// links are relationships, the one numbered list has its own numbering that restarts at 1
	rels := parts["word/_rels/document.xml.rels"]
	if !strings.Contains(rels, `Target="https://example.com/docs"`) || !strings.Contains(rels, `TargetMode="External"`) {
		t.Errorf("rels: %s", rels)
	}
	if !strings.Contains(parts["word/numbering.xml"], `<w:num w:numId="3">`) || !strings.Contains(doc, `<w:numId w:val="3"/>`) || !strings.Contains(doc, `<w:numId w:val="1"/>`) {
		t.Error("list numbering")
	}
	// the nested bullets are one level deeper
	if !strings.Contains(doc, `<w:ilvl w:val="1"/>`) {
		t.Error("nested list level")
	}
	if !strings.Contains(parts["docProps/core.xml"], "Grocery Restocking") {
		t.Error("title")
	}
	again, _ := render.DOCX(reference(t), render.Options{Footer: "Made from revision 7"})
	if !bytes.Equal(b, again) {
		t.Error("not deterministic")
	}
}

func TestDOCXEscapesWhatWouldBreakXML(t *testing.T) {
	b, err := render.DOCX("# A & B <c>\n\nx < y && \"z\" ]]> done\n\n[a&b](https://x.test/?a=1&b=2)\n", render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	parts := unzip(t, b)
	for name, body := range parts {
		d := xml.NewDecoder(strings.NewReader(body))
		for {
			if _, err := d.Token(); err != nil {
				if err != io.EOF {
					t.Fatalf("%s: %v", name, err)
				}
				break
			}
		}
	}
	if !strings.Contains(parts["word/_rels/document.xml.rels"], "a=1&amp;b=2") {
		t.Errorf("link target: %s", parts["word/_rels/document.xml.rels"])
	}
}

// An independent reader: macOS can open the file and finds the text.
func TestDOCXOpensInAnotherProgram(t *testing.T) {
	textutil, err := exec.LookPath("textutil")
	if err != nil {
		t.Skip("textutil is not available")
	}
	b, _ := render.DOCX(reference(t), render.Options{})
	dir := t.TempDir()
	f := filepath.Join(dir, "d.docx")
	if err := os.WriteFile(f, b, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(textutil, "-convert", "txt", "-stdout", f).CombinedOutput()
	if err != nil {
		t.Fatalf("textutil: %v: %s", err, out)
	}
	for _, want := range []string{"Grocery Restocking", "Recurring orders", "Skipping the next order takes one tap.", "Order line", "type Money int64"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("textutil did not find %q in:\n%s", want, out)
		}
	}
}

func TestPDF(t *testing.T) {
	if render.FindChrome(render.Options{}) == "" {
		_, err := render.PDF(context.Background(), "# x", render.Options{})
		if !errors.Is(err, render.ErrNoBrowser) {
			t.Fatalf("without a browser: %v", err)
		}
		t.Skip("no Chromium: set RENDER_CHROME to run the PDF test")
	}
	b, err := render.PDF(context.Background(), reference(t), render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF-")) || len(b) < 2000 {
		t.Errorf("a PDF of %d bytes starting %q", len(b), b[:8])
	}
	// a cancelled context stops it
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := render.PDF(ctx, "# x", render.Options{}); err == nil {
		t.Error("a cancelled context should fail")
	}
}

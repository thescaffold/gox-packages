package render

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// DOCX renders Markdown as a Word document: headings, paragraphs, bold, italic,
// strikethrough, code, links, bullet and numbered lists (nested), task lists,
// tables, block quotes, code blocks and rules. Images become their description.
// Raw HTML is dropped. The file is a plain OOXML package with no macros and no
// external parts but the links.
func DOCX(markdown string, o Options) ([]byte, error) {
	if strings.TrimSpace(markdown) == "" {
		return nil, ErrEmpty
	}
	src := []byte(markdown)
	root := md.Parser().Parse(text.NewReader(src))
	title := o.Title
	if title == "" {
		title = firstHeading(markdown)
	}
	w := &docWriter{src: src, nextNum: 3}
	w.blocks(root, 0)
	if o.Footer != "" {
		w.para("Footer", w.run(o.Footer, runProps{}))
	}

	var out bytes.Buffer
	z := zip.NewWriter(&out)
	add := func(name, body string) error {
		// a fixed time keeps the file the same for the same text
		f, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			return err
		}
		_, err = f.Write([]byte(body))
		return err
	}
	lang := o.Lang
	if lang == "" {
		lang = "en"
	}
	parts := []struct{ name, body string }{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rootRels},
		{"docProps/core.xml", coreXML(title)},
		{"word/document.xml", w.document()},
		{"word/styles.xml", strings.Replace(stylesXML, "{{lang}}", xmlAttr(lang), 1)},
		{"word/numbering.xml", w.numbering()},
		{"word/_rels/document.xml.rels", w.rels()},
	}
	for _, p := range parts {
		if err := add(p.name, p.body); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type runProps struct {
	bold, italic, strike, code, link bool
}

type docWriter struct {
	src     []byte
	body    strings.Builder
	links   []string
	nextNum int
	// ordered lists each get their own numbering instance so they start at 1
	orderedNums []int
}

func (w *docWriter) document() string {
	return xml.Header + `<w:document xmlns:w="` + nsW + `" xmlns:r="` + nsR + `"><w:body>` + w.body.String() +
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1134" w:right="1134" w:bottom="1134" w:left="1134" w:header="567" w:footer="567" w:gutter="0"/></w:sectPr></w:body></w:document>`
}

const (
	nsW = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	nsR = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
)

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func xmlAttr(s string) string { return esc(s) }

func (w *docWriter) run(s string, p runProps) string {
	if s == "" {
		return ""
	}
	var pr strings.Builder
	if p.link {
		pr.WriteString(`<w:rStyle w:val="Hyperlink"/>`)
	}
	if p.code {
		pr.WriteString(`<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:cs="Consolas"/><w:shd w:val="clear" w:color="auto" w:fill="F2F2F2"/>`)
	}
	if p.bold {
		pr.WriteString(`<w:b/>`)
	}
	if p.italic {
		pr.WriteString(`<w:i/>`)
	}
	if p.strike {
		pr.WriteString(`<w:strike/>`)
	}
	rpr := ""
	if pr.Len() > 0 {
		rpr = "<w:rPr>" + pr.String() + "</w:rPr>"
	}
	return `<w:r>` + rpr + `<w:t xml:space="preserve">` + esc(s) + `</w:t></w:r>`
}

func (w *docWriter) para(style string, inner string) { w.paraWith(style, "", inner) }

func (w *docWriter) paraWith(style, extraPPr, inner string) {
	ppr := ""
	if style != "" || extraPPr != "" {
		s := ""
		if style != "" {
			s = `<w:pStyle w:val="` + style + `"/>`
		}
		ppr = "<w:pPr>" + s + extraPPr + "</w:pPr>"
	}
	w.body.WriteString("<w:p>" + ppr + inner + "</w:p>")
}

// inlines renders the children of a block as runs.
func (w *docWriter) inlines(n ast.Node, p runProps) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch x := c.(type) {
		case *ast.Text:
			b.WriteString(w.run(string(x.Segment.Value(w.src)), p))
			if x.HardLineBreak() {
				b.WriteString(`<w:r><w:br/></w:r>`)
			} else if x.SoftLineBreak() {
				b.WriteString(w.run(" ", p))
			}
		case *ast.String:
			b.WriteString(w.run(string(x.Value), p))
		case *ast.CodeSpan:
			q := p
			q.code = true
			b.WriteString(w.run(plain(x, w.src), q))
		case *ast.Emphasis:
			q := p
			if x.Level >= 2 {
				q.bold = true
			} else {
				q.italic = true
			}
			b.WriteString(w.inlines(x, q))
		case *east.Strikethrough:
			q := p
			q.strike = true
			b.WriteString(w.inlines(x, q))
		case *ast.Link:
			w.links = append(w.links, string(x.Destination))
			q := p
			q.link = true
			b.WriteString(`<w:hyperlink r:id="rId` + fmt.Sprint(len(w.links)+10) + `" w:history="1">` + w.inlines(x, q) + `</w:hyperlink>`)
		case *ast.AutoLink:
			w.links = append(w.links, string(x.URL(w.src)))
			q := p
			q.link = true
			b.WriteString(`<w:hyperlink r:id="rId` + fmt.Sprint(len(w.links)+10) + `" w:history="1">` + w.run(string(x.Label(w.src)), q) + `</w:hyperlink>`)
		case *ast.Image:
			b.WriteString(w.run("["+plain(x, w.src)+"]", p))
		case *east.TaskCheckBox:
			if x.IsChecked {
				b.WriteString(w.run("☑ ", p))
			} else {
				b.WriteString(w.run("☐ ", p))
			}
		case *ast.RawHTML:
			// dropped
		default:
			b.WriteString(w.inlines(c, p))
		}
	}
	return b.String()
}

func plain(n ast.Node, src []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch x := c.(type) {
		case *ast.Text:
			b.Write(x.Segment.Value(src))
		case *ast.String:
			b.Write(x.Value)
		default:
			b.WriteString(plain(c, src))
		}
	}
	return b.String()
}

// blocks writes the block children of n. depth is the list nesting level.
func (w *docWriter) blocks(n ast.Node, depth int) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		w.block(c, depth, "")
	}
}

func (w *docWriter) block(c ast.Node, depth int, quote string) {
	switch x := c.(type) {
	case *ast.Heading:
		level := x.Level
		if level > 6 {
			level = 6
		}
		w.para(fmt.Sprintf("Heading%d", level), w.inlines(x, runProps{}))
	case *ast.Paragraph, *ast.TextBlock:
		style := "Normal"
		if quote != "" {
			style = quote
		}
		w.para(style, w.inlines(x, runProps{}))
	case *ast.List:
		w.list(x, depth)
	case *ast.Blockquote:
		for q := x.FirstChild(); q != nil; q = q.NextSibling() {
			w.block(q, depth, "Quote")
		}
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		lines := x.Lines()
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			w.para("Code", w.run(strings.TrimRight(string(seg.Value(w.src)), "\r\n"), runProps{}))
		}
	case *ast.ThematicBreak:
		w.paraWith("", `<w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="808080"/></w:pBdr>`, "")
	case *east.Table:
		w.table(x)
	case *ast.HTMLBlock:
		// dropped
	default:
		w.blocks(c, depth)
	}
}

func (w *docWriter) list(l *ast.List, depth int) {
	numID := 1
	if l.IsOrdered() {
		numID = w.nextNum
		w.nextNum++
		w.orderedNums = append(w.orderedNums, numID)
	}
	if depth > 3 {
		depth = 3
	}
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		first := true
		for c := item.FirstChild(); c != nil; c = c.NextSibling() {
			switch c.(type) {
			case *ast.List:
				w.list(c.(*ast.List), depth+1)
			case *ast.Paragraph, *ast.TextBlock:
				num := fmt.Sprintf(`<w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%d"/></w:numPr>`, depth, numID)
				if !first {
					num = fmt.Sprintf(`<w:ind w:left="%d"/>`, 720*(depth+1))
				}
				w.paraWith("ListParagraph", num, w.inlines(c, runProps{}))
				first = false
			default:
				w.block(c, depth+1, "")
			}
		}
	}
}

func (w *docWriter) table(t *east.Table) {
	cols := 0
	for r := t.FirstChild(); r != nil; r = r.NextSibling() {
		n := 0
		for c := r.FirstChild(); c != nil; c = c.NextSibling() {
			n++
		}
		if n > cols {
			cols = n
		}
	}
	if cols == 0 {
		return
	}
	w.body.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="5000" w:type="pct"/></w:tblPr><w:tblGrid>`)
	for i := 0; i < cols; i++ {
		w.body.WriteString(fmt.Sprintf(`<w:gridCol w:w="%d"/>`, 9000/cols))
	}
	w.body.WriteString(`</w:tblGrid>`)
	for r := t.FirstChild(); r != nil; r = r.NextSibling() {
		header := false
		if _, ok := r.(*east.TableHeader); ok {
			header = true
		}
		w.tableRow(r, header, cols)
	}
	w.body.WriteString(`</w:tbl>`)
	// Word wants a paragraph between a table and whatever follows
	w.body.WriteString(`<w:p/>`)
}

func (w *docWriter) tableRow(r ast.Node, header bool, cols int) {
	w.body.WriteString(`<w:tr>`)
	if header {
		// repeat the header on every page
		w.body.WriteString(`<w:trPr><w:tblHeader/></w:trPr>`)
	}
	n := 0
	for c := r.FirstChild(); c != nil; c = c.NextSibling() {
		n++
		w.body.WriteString(`<w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/></w:tcPr><w:p>`)
		w.body.WriteString(w.inlines(c, runProps{bold: header}))
		w.body.WriteString(`</w:p></w:tc>`)
	}
	for ; n < cols; n++ {
		w.body.WriteString(`<w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/></w:tcPr><w:p/></w:tc>`)
	}
	w.body.WriteString(`</w:tr>`)
}

func (w *docWriter) rels() string {
	var b strings.Builder
	b.WriteString(xml.Header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	b.WriteString(`<Relationship Id="rId1" Type="` + nsR + `/styles" Target="styles.xml"/>`)
	b.WriteString(`<Relationship Id="rId2" Type="` + nsR + `/numbering" Target="numbering.xml"/>`)
	for i, l := range w.links {
		b.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="%s/hyperlink" Target="%s" TargetMode="External"/>`, i+11, nsR, esc(l)))
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

// numbering has one bullet list (numId 1), and one numbered list per ordered list of the document.
func (w *docWriter) numbering() string {
	var b strings.Builder
	b.WriteString(xml.Header + `<w:numbering xmlns:w="` + nsW + `">`)
	b.WriteString(abstractNum(0, "bullet", []string{"•", "◦", "▪", "–"}))
	b.WriteString(abstractNum(1, "decimal", []string{"%1.", "%2.", "%3.", "%4."}))
	b.WriteString(`<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>`)
	for _, id := range w.orderedNums {
		b.WriteString(fmt.Sprintf(`<w:num w:numId="%d"><w:abstractNumId w:val="1"/><w:lvlOverride w:ilvl="0"><w:startOverride w:val="1"/></w:lvlOverride></w:num>`, id))
	}
	b.WriteString(`</w:numbering>`)
	return b.String()
}

func abstractNum(id int, format string, marks []string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<w:abstractNum w:abstractNumId="%d"><w:multiLevelType w:val="hybridMultilevel"/>`, id))
	for i, m := range marks {
		b.WriteString(fmt.Sprintf(`<w:lvl w:ilvl="%d"><w:start w:val="1"/><w:numFmt w:val="%s"/><w:lvlText w:val="%s"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="%d" w:hanging="360"/></w:pPr></w:lvl>`, i, format, esc(m), 720*(i+1)))
	}
	b.WriteString(`</w:abstractNum>`)
	return b.String()
}

func coreXML(title string) string {
	return xml.Header + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>` + esc(title) + `</dc:title><dc:creator>Origine</dc:creator></cp:coreProperties>`
}

const contentTypes = xml.Header + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/><Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>`

const rootRels = xml.Header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/></Relationships>`

var stylesXML = xml.Header + `<w:styles xmlns:w="` + nsW + `"><w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:cs="Calibri"/><w:sz w:val="22"/><w:lang w:val="{{lang}}"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +
	`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>` +
	h(1, 40, 360) + h(2, 32, 280) + h(3, 28, 240) + h(4, 24, 200) + h(5, 22, 200) + h(6, 22, 200) +
	`<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:spacing w:after="60"/></w:pPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:pBdr><w:left w:val="single" w:sz="18" w:space="8" w:color="BFBFBF"/></w:pBdr><w:ind w:left="360"/></w:pPr><w:rPr><w:i/><w:color w:val="595959"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Code"><w:name w:val="Code"/><w:basedOn w:val="Normal"/><w:pPr><w:shd w:val="clear" w:color="auto" w:fill="F2F2F2"/><w:spacing w:after="0" w:line="240" w:lineRule="auto"/></w:pPr><w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:cs="Consolas"/><w:sz w:val="20"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Footer"><w:name w:val="footer"/><w:basedOn w:val="Normal"/><w:rPr><w:color w:val="595959"/><w:sz w:val="18"/></w:rPr></w:style>` +
	`<w:style w:type="character" w:styleId="Hyperlink"><w:name w:val="Hyperlink"/><w:rPr><w:color w:val="0563C1"/><w:u w:val="single"/></w:rPr></w:style>` +
	`<w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/><w:left w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/><w:bottom w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/><w:right w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/><w:insideH w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/><w:insideV w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/></w:tblBorders><w:tblCellMar><w:left w:w="100" w:type="dxa"/><w:right w:w="100" w:type="dxa"/></w:tblCellMar></w:tblPr></w:style></w:styles>`

func h(level, size, before int) string {
	return fmt.Sprintf(`<w:style w:type="paragraph" w:styleId="Heading%d"><w:name w:val="heading %d"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="%d" w:after="120"/><w:outlineLvl w:val="%d"/></w:pPr><w:rPr><w:b/><w:sz w:val="%d"/></w:rPr></w:style>`, level, level, before, level-1, size)
}

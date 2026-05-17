// Package render parses markdown to HTML using goldmark with GFM features
// and source-line annotations used by the browser scroll-sync client.
package render

import (
	"bytes"
	"context"
	"fmt"
	gohtml "html"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/aldevv/md-preview/internal/render/pandoc"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var dataLineAttr = []byte("data-line")

// newMarkdown wraps the thematic-break and fenced-code parsers in a
// lineRecorder because goldmark's defaults don't populate Lines() for HRs
// and skip the opening fence on fenced code.
//
// WithUnsafe is intentionally NOT enabled: raw HTML in markdown would
// execute as scripts in the localhost-bound preview origin, giving any
// cloned README drive-by access to the local browser session.
func newMarkdown(sourceDir string) goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(extension.GFM, Alerts, Math),
		goldmark.WithParserOptions(
			parser.WithBlockParsers(
				util.Prioritized(&lineRecorder{inner: parser.NewThematicBreakParser()}, 100),
				util.Prioritized(&lineRecorder{inner: parser.NewFencedCodeBlockParser()}, 600),
			),
		),
		goldmark.WithRendererOptions(
			renderer.WithNodeRenderers(util.Prioritized(newDataLineRenderer(sourceDir), 100)),
		),
	)
}

// dataLineRenderer overrides goldmark's default code-block rendering
// so the generated <pre> can carry a data-line attribute (the default
// funcs drop node attributes).
type dataLineRenderer struct {
	html.Config
	// sourceDir is the markdown file's directory, threaded through to
	// pandoc.Render so \input{} in fenced LaTeX resolves relative to
	// the document instead of mdp's CWD. Empty for RenderBytes callers
	// with no file backing.
	sourceDir string
}

func newDataLineRenderer(sourceDir string) *dataLineRenderer {
	return &dataLineRenderer{Config: html.NewConfig(), sourceDir: sourceDir}
}

func (d *dataLineRenderer) SetOption(name renderer.OptionName, value any) {
	d.Config.SetOption(name, value)
}

func (d *dataLineRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, d.renderFencedCodeBlock)
	reg.Register(ast.KindCodeBlock, d.renderCodeBlock)
	// KindHTMLBlock deliberately not registered. goldmark's default with
	// WithUnsafe off emits a "raw HTML omitted" comment, which is what we want.
}

func writeDataLineAttr(w util.BufWriter, n ast.Node) {
	if v, ok := n.Attribute(dataLineAttr); ok {
		_, _ = w.WriteString(` data-line="`)
		switch typed := v.(type) {
		case []byte:
			_, _ = w.Write(typed)
		case string:
			_, _ = w.WriteString(typed)
		}
		_ = w.WriteByte('"')
	}
}

func (d *dataLineRenderer) writeLines(w util.BufWriter, source []byte, n ast.Node) {
	l := n.Lines().Len()
	for i := 0; i < l; i++ {
		line := n.Lines().At(i)
		d.Config.Writer.RawWrite(w, line.Value(source))
	}
}

func (d *dataLineRenderer) renderFencedCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.FencedCodeBlock)
	if format := pandocFenceFormat(n.Language(source)); format != "" {
		// Called twice (entering + leaving); emit the whole <div> once
		// on entering. Fence bodies are leaf text, no child AST nodes.
		if entering {
			d.emitPandocFence(w, source, n, format)
		}
		return ast.WalkContinue, nil
	}
	if isMermaidLang(n.Language(source)) {
		if entering {
			d.emitMermaidFence(w, source, n)
		}
		return ast.WalkContinue, nil
	}
	if entering {
		_, _ = w.WriteString("<pre")
		writeDataLineAttr(w, node)
		_, _ = w.WriteString("><code")
		language := n.Language(source)
		if language != nil {
			_, _ = w.WriteString(` class="language-`)
			d.Config.Writer.Write(w, language)
			_, _ = w.WriteString(`"`)
		}
		_ = w.WriteByte('>')
		d.writeLines(w, source, n)
	} else {
		_, _ = w.WriteString("</code></pre>\n")
	}
	return ast.WalkContinue, nil
}

func isMermaidLang(language []byte) bool {
	if language == nil {
		return false
	}
	return strings.EqualFold(string(language), "mermaid")
}

// The page template gates mermaid.js on the presence of pre.mermaid,
// then auto-runs it to swap each block for an inline SVG.
func (d *dataLineRenderer) emitMermaidFence(w util.BufWriter, source []byte, n *ast.FencedCodeBlock) {
	_, _ = w.WriteString(`<pre class="mermaid"`)
	writeDataLineAttr(w, n)
	_, _ = w.WriteString(`>`)
	for i := 0; i < n.Lines().Len(); i++ {
		line := n.Lines().At(i)
		_, _ = w.WriteString(gohtml.EscapeString(string(line.Value(source))))
	}
	_, _ = w.WriteString("</pre>\n")
}

func pandocFenceFormat(language []byte) string {
	if language == nil {
		return ""
	}
	switch strings.ToLower(string(language)) {
	case "latex", "tex", "pandoc-latex":
		return "latex"
	case "typst", "typ":
		return "typst"
	}
	return ""
}

// On render failure the error message goes into a .pandoc-error div
// so the preview surfaces it instead of silently dropping the block.
func (d *dataLineRenderer) emitPandocFence(w util.BufWriter, source []byte, n *ast.FencedCodeBlock, format string) {
	var body bytes.Buffer
	for i := 0; i < n.Lines().Len(); i++ {
		line := n.Lines().At(i)
		body.Write(line.Value(source))
	}
	dataLine := ""
	if v, ok := n.Attribute(dataLineAttr); ok {
		switch typed := v.(type) {
		case []byte:
			dataLine = string(typed)
		case string:
			dataLine = typed
		}
	}
	rendered, err := pandoc.Render(context.Background(), body.Bytes(), d.sourceDir, format)
	if err != nil {
		_, _ = w.WriteString(`<div class="pandoc-error"`)
		if dataLine != "" {
			_, _ = w.WriteString(` data-line="`)
			_, _ = w.WriteString(dataLine)
			_, _ = w.WriteString(`"`)
		}
		_, _ = w.WriteString(`>`)
		_, _ = w.WriteString(format)
		_, _ = w.WriteString(` render error: `)
		_, _ = w.WriteString(gohtml.EscapeString(err.Error()))
		_, _ = w.WriteString("</div>\n")
		return
	}
	_, _ = w.WriteString(`<div class="pandoc-block"`)
	if dataLine != "" {
		_, _ = w.WriteString(` data-line="`)
		_, _ = w.WriteString(dataLine)
		_, _ = w.WriteString(`"`)
	}
	_, _ = w.WriteString(`>`)
	_, _ = w.WriteString(rendered)
	_, _ = w.WriteString("</div>\n")
}

func (d *dataLineRenderer) renderCodeBlock(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<pre")
		writeDataLineAttr(w, n)
		_, _ = w.WriteString("><code>")
		d.writeLines(w, source, n)
	} else {
		_, _ = w.WriteString("</code></pre>\n")
	}
	return ast.WalkContinue, nil
}

// lineRecorder stamps data-line at Open() time for parsers whose
// Lines() omits the opening line (thematic break, fenced code fence).
type lineRecorder struct {
	inner parser.BlockParser
}

func (h *lineRecorder) Trigger() []byte { return h.inner.Trigger() }

func (h *lineRecorder) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.Position()
	node, state := h.inner.Open(parent, reader, pc)
	if node != nil {
		node.SetAttribute(dataLineAttr, []byte(strconv.Itoa(line+1)))
	}
	return node, state
}

func (h *lineRecorder) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	return h.inner.Continue(node, reader, pc)
}

func (h *lineRecorder) Close(node ast.Node, reader text.Reader, pc parser.Context) {
	h.inner.Close(node, reader, pc)
}

func (h *lineRecorder) CanInterruptParagraph() bool { return h.inner.CanInterruptParagraph() }
func (h *lineRecorder) CanAcceptIndentedLine() bool { return h.inner.CanAcceptIndentedLine() }

func stripFrontmatter(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return content
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return content
	}
	return strings.Join(lines[end+1:], "\n")
}

type lineIndex struct {
	starts []int
}

func buildLineIndex(source []byte) *lineIndex {
	starts := make([]int, 0, bytes.Count(source, []byte{'\n'})+1)
	starts = append(starts, 0)
	for i, b := range source {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &lineIndex{starts: starts}
}

func (li *lineIndex) lineOf(offset int) int {
	idx := sort.SearchInts(li.starts, offset+1) - 1
	if idx < 0 {
		idx = 0
	}
	return idx + 1
}

// list/list-item nodes wrap children without Lines() of their own, so
// fall back to descending into children. Returns -1 when no source
// offset is reachable.
func firstSourceOffset(n ast.Node) int {
	if n == nil {
		return -1
	}
	if lines := n.Lines(); lines != nil && lines.Len() > 0 {
		return lines.At(0).Start
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if off := firstSourceOffset(c); off >= 0 {
			return off
		}
	}
	return -1
}

func shouldAnnotate(n ast.Node) bool {
	switch n.Kind() {
	case ast.KindHeading,
		ast.KindParagraph,
		ast.KindBlockquote,
		ast.KindList,
		ast.KindListItem,
		ast.KindFencedCodeBlock,
		ast.KindCodeBlock,
		ast.KindThematicBreak:
		return true
	case extast.KindTable,
		extast.KindTableHeader,
		extast.KindTableRow,
		extast.KindTableCell:
		return true
	}
	return false
}

// Nodes already annotated by a custom block parser (thematic break,
// fenced code fence) are skipped so the parser-recorded line wins.
func annotateLines(doc ast.Node, source []byte) {
	li := buildLineIndex(source)
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || !shouldAnnotate(n) {
			return ast.WalkContinue, nil
		}
		if _, already := n.Attribute(dataLineAttr); already {
			return ast.WalkContinue, nil
		}
		off := firstSourceOffset(n)
		if off < 0 {
			return ast.WalkContinue, nil
		}
		line := li.lineOf(off)
		n.SetAttribute(dataLineAttr, []byte(strconv.Itoa(line)))
		return ast.WalkContinue, nil
	})
}

// sourceDir threads to fenced LaTeX so \input{} resolves relative to
// the source file's directory.
func renderHTML(source []byte, sourceDir string) string {
	md := newMarkdown(sourceDir)
	reader := text.NewReader(source)
	doc := md.Parser().Parse(reader, parser.WithContext(parser.NewContext()))
	annotateLines(doc, source)
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, source, doc); err != nil {
		return fmt.Sprintf("<p>Error rendering: %s</p>", gohtml.EscapeString(err.Error()))
	}
	return buf.String()
}

// RenderBody returns an HTML body with 1-indexed data-line="N" on
// every block whose origin can be traced. Extensions in
// pandoc.InputFormat dispatch to pandoc; everything else goes through
// goldmark after YAML-frontmatter stripping. Returns pandoc.ErrNotFound
// when the pandoc dispatch path needs the binary and it's absent.
func RenderBody(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("<p>Error reading file: %s</p>", gohtml.EscapeString(err.Error())), err
	}
	if format := pandoc.InputFormat(path); format != "" {
		rendered, err := pandoc.Render(context.Background(), content, filepath.Dir(path), format)
		if err != nil {
			return "", err
		}
		return `<div class="pandoc-block">` + rendered + `</div>`, nil
	}
	sourceDir := filepath.Dir(path)
	stripped := stripFrontmatter(string(content))
	return renderHTML([]byte(stripped), sourceDir), nil
}

// RenderBytes is the in-memory variant. Fenced LaTeX's \input{}
// resolution won't work since there's no source file backing it.
func RenderBytes(content []byte) string {
	stripped := stripFrontmatter(string(content))
	return renderHTML([]byte(stripped), "")
}

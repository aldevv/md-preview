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

// dataLineRenderer overrides goldmark's default rendering for code blocks
// (whose default funcs ignore node attributes) so we can stamp data-line
// on the generated <pre>.
type dataLineRenderer struct {
	html.Config
	// sourceDir is the directory of the source markdown file; passed to
	// pandoc.Render so \input{} in fenced LaTeX resolves relative to
	// the document, not to mdp's CWD. Empty for tests / RenderBytes
	// callers that don't have a file backing them.
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
	// KindHTMLBlock deliberately not registered — goldmark's default with
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
		// Both entering and !entering pass through here; emit the full
		// <div>...</div> on entering and a no-op on closing. Children of
		// a FencedCodeBlock are body lines (no separate AST nodes), so
		// no walk-status special-casing is needed.
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

// isMermaidLang reports whether the fence info string requests
// client-side mermaid rendering. Case-insensitive match on "mermaid".
func isMermaidLang(language []byte) bool {
	if language == nil {
		return false
	}
	return strings.EqualFold(string(language), "mermaid")
}

// emitMermaidFence emits a `<pre class="mermaid" data-line=...>`
// wrapping the raw fence body. mermaid.js (loaded conditionally by
// the page template when this class is present) auto-runs on page
// load and replaces each block with an inline SVG diagram.
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

// pandocFenceFormat maps a fenced-code info string to the pandoc
// --from name it should be rendered with, or "" if the fence is
// just regular code (passes through to the normal highlighter).
// Recognized: latex/tex/pandoc-latex (→ latex), typst (→ typst).
// Other formats with viable embedded-fence semantics can be added
// here; not every pandoc input format makes sense embedded in
// markdown.
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

// emitPandocFence renders the fence body via host pandoc using the
// given --from format. On error the user-visible message is dropped
// into a .pandoc-error div so the preview surfaces the failure
// instead of silently dropping the block.
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

// lineRecorder wraps a BlockParser to stamp data-line on the opened node
// using the reader's position at Open time. Used for kinds whose Lines()
// don't include the opening line (e.g. thematic break, fenced code fence).
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

// stripFrontmatter drops a leading YAML frontmatter block: if the first line
// is "---", strip through the next "---" line. If no closing "---" is found,
// return content unchanged.
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

// lineIndex stores the byte offset of every line start in source so we can
// resolve a byte offset to a 1-indexed line via binary search.
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

// lineOf returns a 1-indexed line number for the given byte offset.
func (li *lineIndex) lineOf(offset int) int {
	idx := sort.SearchInts(li.starts, offset+1) - 1
	if idx < 0 {
		idx = 0
	}
	return idx + 1
}

// firstSourceOffset returns the earliest source byte offset reachable from n
// via Lines(). Falls back to descendants since list/list-item nodes wrap
// children without Lines of their own. Returns -1 if none found.
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

// annotateLines walks the AST and sets data-line on every block node whose
// origin can be traced to a source line. Nodes already annotated by a
// custom block parser (e.g. thematic break, fenced code fence) are left
// alone so the parser-recorded line wins.
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

// renderHTML parses the markdown source and renders it with line annotations.
// sourceDir is threaded into fenced LaTeX rendering so \input{} resolves
// relative to the source file's directory.
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

// RenderBody reads filepath, strips YAML frontmatter, and renders it
// to an HTML body with data-line="N" attributes (1-indexed) on every
// block-level open tag whose origin can be traced to a source line.
//
// On read error, returns an HTML body and a non-nil error so callers
// can decide whether to surface the failure. On pandoc-not-found for
// pandoc-routed content, returns pandoc.ErrNotFound so the caller can
// print a usable install hint and exit non-zero.
//
// File extension dispatches the renderer: any extension recognized by
// pandoc.InputFormat goes through pandoc; everything else uses
// goldmark.
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

// RenderBytes is RenderBody for already-loaded content. Useful for tests and
// in-memory callers. The fenced-LaTeX intercept won't be able to resolve
// \input{} relative paths since the caller didn't supply a sourceDir.
func RenderBytes(content []byte) string {
	stripped := stripFrontmatter(string(content))
	return renderHTML([]byte(stripped), "")
}

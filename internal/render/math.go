package render

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// mathInlineNode carries inline math markup (with delimiters intact)
// past goldmark's backslash-escape parser so the page-template
// KaTeX auto-render picks it up unchanged.
type mathInlineNode struct {
	ast.BaseInline
	display bool
	raw     []byte
}

var kindMathInline = ast.NewNodeKind("MathInline")

func (n *mathInlineNode) Kind() ast.NodeKind                  { return kindMathInline }
func (n *mathInlineNode) Dump(source []byte, level int)       { ast.DumpHelper(n, source, level, nil, nil) }

// mathBlockNode is the block-level cousin used by the block parser
// (which must return a Block AST node, not an Inline one).
type mathBlockNode struct {
	ast.BaseBlock
	raw []byte
}

var kindMathBlock = ast.NewNodeKind("MathBlock")

func (n *mathBlockNode) Kind() ast.NodeKind             { return kindMathBlock }
func (n *mathBlockNode) Dump(source []byte, level int)  { ast.DumpHelper(n, source, level, nil, nil) }

const (
	mathInline  = false
	mathDisplay = true
)

// mathInlineParser recognizes single-line `\(...\)`, `\[...\]`,
// `$...$`, `$$...$$` math at priority above the default
// BackslashEscapeParser so the leading `\` doesn't get stripped.
type mathInlineParser struct{}

func (p *mathInlineParser) Trigger() []byte { return []byte{'\\', '$'} }

func (p *mathInlineParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) < 2 {
		return nil
	}
	switch line[0] {
	case '\\':
		switch line[1] {
		case '(':
			return parseDelimited(block, line, []byte(`\(`), []byte(`\)`), mathInline)
		case '[':
			return parseDelimited(block, line, []byte(`\[`), []byte(`\]`), mathDisplay)
		}
	case '$':
		if len(line) >= 2 && line[1] == '$' {
			return parseDelimited(block, line, []byte("$$"), []byte("$$"), mathDisplay)
		}
		return parseDollarInline(block, line)
	}
	return nil
}

func parseDelimited(block text.Reader, line, open, close []byte, display bool) ast.Node {
	rest := line[len(open):]
	end := bytes.Index(rest, close)
	if end < 0 {
		return nil
	}
	if end == 0 && bytes.Equal(open, close) {
		return nil
	}
	raw := make([]byte, 0, len(open)+end+len(close))
	raw = append(raw, open...)
	raw = append(raw, rest[:end]...)
	raw = append(raw, close...)
	block.Advance(len(raw))
	return &mathInlineNode{display: display, raw: raw}
}

func parseDollarInline(block text.Reader, line []byte) ast.Node {
	if len(line) < 3 {
		return nil
	}
	rest := line[1:]
	end := bytes.IndexByte(rest, '$')
	if end <= 0 {
		return nil
	}
	body := rest[:end]
	if isSpace(body[0]) || isSpace(body[len(body)-1]) {
		return nil
	}
	raw := make([]byte, 0, len(body)+2)
	raw = append(raw, '$')
	raw = append(raw, body...)
	raw = append(raw, '$')
	block.Advance(len(raw))
	return &mathInlineNode{display: mathInline, raw: raw}
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// mathBlockParser recognizes `$$...$$` and `\[...\]` blocks owning
// their own line(s), single-line or multi-line.
type mathBlockParser struct{}

func (b *mathBlockParser) Trigger() []byte { return []byte{'$', '\\'} }

func (b *mathBlockParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	trim := bytes.TrimRight(line, " \t\r\n")
	var open, close []byte
	switch {
	case bytes.HasPrefix(trim, []byte("$$")):
		open, close = []byte("$$"), []byte("$$")
	case bytes.HasPrefix(trim, []byte(`\[`)):
		open, close = []byte(`\[`), []byte(`\]`)
	default:
		return nil, parser.NoChildren
	}
	// Single-line form: `$$ x $$` on its own line.
	if bytes.HasSuffix(trim, close) && len(trim) > len(open)+len(close) {
		raw := append([]byte{}, trim...)
		reader.Advance(len(line))
		n := &mathBlockNode{raw: raw}
		parent.AppendChild(parent, n)
		return n, parser.NoChildren
	}
	// Otherwise the opener line must be just the open marker.
	if !bytes.Equal(trim, open) {
		return nil, parser.NoChildren
	}
	reader.Advance(len(line))
	var body bytes.Buffer
	body.Write(open)
	body.WriteByte('\n')
	for {
		l, _ := reader.PeekLine()
		if len(l) == 0 {
			return nil, parser.NoChildren
		}
		t := bytes.TrimRight(l, " \t\r\n")
		if bytes.Equal(t, close) {
			body.Write(close)
			reader.Advance(len(l))
			break
		}
		body.Write(l)
		reader.Advance(len(l))
	}
	n := &mathBlockNode{raw: append([]byte{}, body.Bytes()...)}
	parent.AppendChild(parent, n)
	return n, parser.NoChildren
}

func (b *mathBlockParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	return parser.Close
}
func (b *mathBlockParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}
func (b *mathBlockParser) CanInterruptParagraph() bool                                { return true }
func (b *mathBlockParser) CanAcceptIndentedLine() bool                                { return false }

// mathRenderer writes math nodes' raw bytes verbatim (with data-line
// stamping for blocks). KaTeX auto-render in the browser handles the
// delimited content.
type mathRenderer struct{}

func (r *mathRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMathInline, r.renderInline)
	reg.Register(kindMathBlock, r.renderBlock)
}

func (r *mathRenderer) renderInline(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*mathInlineNode)
	if n.display {
		_, _ = w.WriteString(`<span class="math-display">`)
	} else {
		_, _ = w.WriteString(`<span class="math-inline">`)
	}
	_, _ = w.Write(n.raw)
	_, _ = w.WriteString(`</span>`)
	return ast.WalkContinue, nil
}

func (r *mathRenderer) renderBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*mathBlockNode)
	_, _ = w.WriteString(`<div class="math-display"`)
	writeDataLineAttr(w, n)
	_, _ = w.WriteString(`>`)
	_, _ = w.Write(n.raw)
	_, _ = w.WriteString(`</div>`)
	return ast.WalkContinue, nil
}

// Math is the goldmark extension wiring the math parsers + renderer.
// Register at priority above the default BackslashEscapeParser (200)
// so `\(`, `\[`, `\,` inside math regions don't get consumed.
var Math goldmark.Extender = mathExtension{}

type mathExtension struct{}

func (mathExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithInlineParsers(util.Prioritized(&mathInlineParser{}, 150)),
		parser.WithBlockParsers(util.Prioritized(&mathBlockParser{}, 150)),
	)
	m.Renderer().AddOptions(
		renderer.WithNodeRenderers(util.Prioritized(&mathRenderer{}, 150)),
	)
}

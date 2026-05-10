package render

import (
	"bytes"
	"regexp"
	"strconv"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Alert is the AST node for a GitHub-style alert blockquote — recognised
// when a blockquote's first line is "[!NOTE]" (or TIP / IMPORTANT / WARNING
// / CAUTION). Renders as `<div class="markdown-alert markdown-alert-...">`.
type Alert struct {
	ast.BaseBlock
	AlertKind []byte
}

// KindAlert is the ast.NodeKind for Alert.
var KindAlert = ast.NewNodeKind("Alert")

func (n *Alert) Kind() ast.NodeKind { return KindAlert }

func (n *Alert) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"AlertKind": string(n.AlertKind)}, nil)
}

// alertHeader matches a blockquote's first-line "[!TYPE]" header.
var alertHeader = regexp.MustCompile(`(?i)^\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*$`)

// alertsExtension wires the AST transformer + renderer into a goldmark
// instance.
type alertsExtension struct{}

// Alerts is the goldmark extension that recognises GitHub-style alerts.
var Alerts goldmark.Extender = alertsExtension{}

func (alertsExtension) Extend(md goldmark.Markdown) {
	md.Parser().AddOptions(parser.WithASTTransformers(
		util.Prioritized(&alertTransformer{}, 100),
	))
	md.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(&alertNodeRenderer{}, 100),
	))
}

// alertTransformer detects blockquote nodes whose first source line is
// "[!TYPE]" and replaces them with Alert nodes carrying the kind.
type alertTransformer struct{}

func (t *alertTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	source := reader.Source()
	var bqs []*ast.Blockquote
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if bq, ok := n.(*ast.Blockquote); ok {
			bqs = append(bqs, bq)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})

	for _, bq := range bqs {
		kind, headerLine := detectAlertKind(bq, source)
		if kind == "" {
			continue
		}
		alert := &Alert{AlertKind: []byte(kind)}
		alert.SetAttribute(dataLineAttr, []byte(strconv.Itoa(headerLine)))

		if first := bq.FirstChild(); first != nil && first.Kind() == ast.KindParagraph {
			stripFirstInlineLine(first)
			if first.ChildCount() == 0 {
				bq.RemoveChild(bq, first)
			}
		}

		for c := bq.FirstChild(); c != nil; {
			next := c.NextSibling()
			bq.RemoveChild(bq, c)
			alert.AppendChild(alert, c)
			c = next
		}

		if parent := bq.Parent(); parent != nil {
			parent.ReplaceChild(parent, bq, alert)
		}
	}
}

// detectAlertKind returns ("", 0) if bq is not an alert. Otherwise returns
// the lowercase kind and the 1-indexed source line of the header.
func detectAlertKind(bq *ast.Blockquote, source []byte) (string, int) {
	first := bq.FirstChild()
	if first == nil || first.Kind() != ast.KindParagraph {
		return "", 0
	}
	lines := first.Lines()
	if lines.Len() == 0 {
		return "", 0
	}
	seg := lines.At(0)
	line := bytes.TrimRight(seg.Value(source), "\n")
	matches := alertHeader.FindSubmatch(line)
	if len(matches) != 2 {
		return "", 0
	}
	kind := string(bytes.ToLower(matches[1]))
	headerLine := bytes.Count(source[:seg.Start], []byte{'\n'}) + 1
	return kind, headerLine
}

// stripFirstInlineLine removes inline children up to and including the first
// soft/hard line break — i.e. drops the `[!TYPE]` line from the paragraph.
// Goldmark joins paragraph lines as Text nodes with SoftLineBreak() set on
// all but the last; brackets/exclamation in `[!TYPE]` are not syntax, so
// the line resolves to plain Text node(s) on a single line.
func stripFirstInlineLine(p ast.Node) {
	var toRemove []ast.Node
	for c := p.FirstChild(); c != nil; c = c.NextSibling() {
		toRemove = append(toRemove, c)
		if t, ok := c.(*ast.Text); ok && (t.SoftLineBreak() || t.HardLineBreak()) {
			break
		}
	}
	for _, n := range toRemove {
		p.RemoveChild(p, n)
	}
}

// alertNodeRenderer emits the HTML for an Alert node.
type alertNodeRenderer struct{}

func (r *alertNodeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindAlert, r.renderAlert)
}

func (r *alertNodeRenderer) renderAlert(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*Alert)
	if entering {
		_, _ = w.WriteString(`<div class="markdown-alert markdown-alert-`)
		_, _ = w.Write(n.AlertKind)
		_ = w.WriteByte('"')
		writeDataLineAttr(w, n)
		_, _ = w.WriteString(">\n")
		_, _ = w.WriteString(`<p class="markdown-alert-title">`)
		if len(n.AlertKind) > 0 {
			_, _ = w.Write(bytes.ToUpper(n.AlertKind[:1]))
			_, _ = w.Write(n.AlertKind[1:])
		}
		_, _ = w.WriteString("</p>\n")
	} else {
		_, _ = w.WriteString("</div>\n")
	}
	return ast.WalkContinue, nil
}

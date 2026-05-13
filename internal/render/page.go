package render

import (
	"fmt"
	"strings"

	"github.com/aldevv/md-preview/internal/render/latex"
)

// CSSDark holds the dark theme CSS custom properties.
const CSSDark = `
:root {
  --color-bg-primary: #0d1117;
  --color-text-primary: #c9d1d9;
  --color-text-secondary: #8b949e;
  --color-border: #30363d;
  --color-bg-code: #161b22;
  --color-link: #58a6ff;
  --color-heading-border: #21262d;
  --color-alert-note: #1f6feb;
  --color-alert-tip: #238636;
  --color-alert-important: #8957e5;
  --color-alert-warning: #d29922;
  --color-alert-caution: #da3633;
}
`

// CSSLight holds the light theme CSS custom properties.
const CSSLight = `
:root {
  --color-bg-primary: #ffffff;
  --color-text-primary: #24292e;
  --color-text-secondary: #586069;
  --color-border: #e1e4e8;
  --color-bg-code: #f6f8fa;
  --color-link: #0366d6;
  --color-heading-border: #eaecef;
  --color-alert-note: #0969da;
  --color-alert-tip: #1a7f37;
  --color-alert-important: #8250df;
  --color-alert-warning: #9a6700;
  --color-alert-caution: #cf222e;
}
`

// CSSCommon holds the shared markdown body styling.
const CSSCommon = `
* { box-sizing: border-box; margin: 0; padding: 0; }
body {
  background: var(--color-bg-primary);
  color: var(--color-text-primary);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
  font-size: 18px;
  line-height: 1.6;
  padding: 32px;
  max-width: 900px;
  margin: 0 auto;
}
.markdown-body h1 { font-size: 2.25em; border-bottom: 1px solid var(--color-heading-border); padding-bottom: 0.3em; margin-bottom: 1em; margin-top: 1.5em; }
.markdown-body h2 { font-size: 1.75em; border-bottom: 1px solid var(--color-heading-border); padding-bottom: 0.3em; margin-bottom: 1em; margin-top: 1.5em; }
.markdown-body h3 { font-size: 1.5em; margin-bottom: 0.75em; margin-top: 1.5em; }
.markdown-body h4 { font-size: 1.25em; margin-bottom: 0.75em; margin-top: 1.5em; }
.markdown-body h5 { font-size: 1em; margin-bottom: 0.75em; margin-top: 1.5em; }
.markdown-body h6 { font-size: 0.875em; color: var(--color-text-secondary); margin-bottom: 0.75em; margin-top: 1.5em; }
.markdown-body p { margin-bottom: 1em; }
.markdown-body a { color: var(--color-link); text-decoration: none; }
.markdown-body a:hover { text-decoration: underline; }
.markdown-body code {
  background: var(--color-bg-code);
  border-radius: 4px;
  font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
  font-size: 85%;
  padding: 0.2em 0.4em;
}
.markdown-body pre {
  background: var(--color-bg-code);
  border-radius: 6px;
  overflow: auto;
  padding: 16px;
  margin-bottom: 1em;
}
.markdown-body pre code {
  background: none;
  padding: 0;
  font-size: 100%;
  white-space: pre;
}
.markdown-body blockquote {
  border-left: 4px solid var(--color-border);
  color: var(--color-text-secondary);
  padding: 0 1em;
  margin-bottom: 1em;
}
.markdown-body ul, .markdown-body ol { padding-left: 2em; margin-bottom: 1em; }
.markdown-body li { margin-bottom: 0.25em; }
.markdown-body table { border-collapse: collapse; width: 100%; margin-bottom: 1em; }
.markdown-body th, .markdown-body td {
  border: 1px solid var(--color-border);
  padding: 6px 13px;
  text-align: left;
}
.markdown-body th { background: var(--color-bg-code); font-weight: 600; }
.markdown-body tr:nth-child(even) { background: var(--color-bg-code); }
.markdown-body hr { border: none; border-top: 1px solid var(--color-border); margin: 1.5em 0; }
.markdown-body img { max-width: 100%; }
.markdown-body .markdown-alert {
  border-left: 4px solid;
  padding: 8px 16px;
  margin-bottom: 1em;
}
.markdown-body .markdown-alert > p { margin-bottom: 0.5em; }
.markdown-body .markdown-alert > p:last-child { margin-bottom: 0; }
.markdown-body .markdown-alert-title {
  font-weight: 600;
  text-transform: uppercase;
  font-size: 0.85em;
  letter-spacing: 0.05em;
}
.markdown-body .markdown-alert-note { border-color: var(--color-alert-note); }
.markdown-body .markdown-alert-note .markdown-alert-title { color: var(--color-alert-note); }
.markdown-body .markdown-alert-tip { border-color: var(--color-alert-tip); }
.markdown-body .markdown-alert-tip .markdown-alert-title { color: var(--color-alert-tip); }
.markdown-body .markdown-alert-important { border-color: var(--color-alert-important); }
.markdown-body .markdown-alert-important .markdown-alert-title { color: var(--color-alert-important); }
.markdown-body .markdown-alert-warning { border-color: var(--color-alert-warning); }
.markdown-body .markdown-alert-warning .markdown-alert-title { color: var(--color-alert-warning); }
.markdown-body .markdown-alert-caution { border-color: var(--color-alert-caution); }
.markdown-body .markdown-alert-caution .markdown-alert-title { color: var(--color-alert-caution); }
`

// highlight.js theme CSS and the highlight.js bundle itself live in
// assets.go (embedded via go:embed) and are inlined by BuildPage below.

// vimKeysScriptTemplate implements hjkl + d/u + g/G + q (close window)
// page navigation, ignoring keys while focus is in an editable element.
// The __DOWN__/__UP__/__RIGHT__ placeholders are replaced with j/k/l
// (qwerty) or n/e/i (colemak); h, d/u, g/G, and q are kept as-is in both
// layouts (either home-row in colemak already or kept for mnemonic).
const vimKeysScriptTemplate = `
(() => {
    const STEP = 60;
    function isEditable(el) {
        if (!el) return false;
        const tag = el.tagName;
        return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable;
    }
    document.addEventListener('keydown', (e) => {
        if (e.ctrlKey || e.metaKey || e.altKey) return;
        if (isEditable(e.target)) return;
        const h = window.innerHeight;
        switch (e.key) {
            case '__DOWN__':  window.scrollBy({ top:  STEP, behavior: 'auto' }); break;
            case '__UP__':    window.scrollBy({ top: -STEP, behavior: 'auto' }); break;
            case 'h':         window.scrollBy({ left: -STEP, behavior: 'auto' }); break;
            case '__RIGHT__': window.scrollBy({ left:  STEP, behavior: 'auto' }); break;
            case 'd': window.scrollBy({ top:  h / 2, behavior: 'smooth' }); break;
            case 'u': window.scrollBy({ top: -h / 2, behavior: 'smooth' }); break;
            case 'g': window.scrollTo({ top: 0, behavior: 'smooth' }); break;
            case 'G': window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'smooth' }); break;
            case 'q': window.close(); break;
            default: return;
        }
        e.preventDefault();
    });
})();
`

// vimKeys returns the navigation script with j/k/l (default) or n/e/i
// (colemak) substituted in.
func vimKeys(colemak bool) string {
	down, up, right := "j", "k", "l"
	if colemak {
		down, up, right = "n", "e", "i"
	}
	s := strings.ReplaceAll(vimKeysScriptTemplate, "__DOWN__", down)
	s = strings.ReplaceAll(s, "__UP__", up)
	s = strings.ReplaceAll(s, "__RIGHT__", right)
	return s
}

// wsScriptTemplate is the WebSocket scroll/reload client; __PORT__ is replaced
// with the server port at runtime.
const wsScriptTemplate = `
function absDocTop(el) {
    let y = 0;
    while (el) { y += el.offsetTop; el = el.offsetParent; }
    return y;
}
let _els = [];
function cacheEls() {
    _els = [...document.querySelectorAll('[data-line]')]
        .map(el => ({ el, line: parseInt(el.dataset.line), top: absDocTop(el) }))
        .sort((a, b) => a.line - b.line);
}
window.addEventListener('load', cacheEls);

const ws = new WebSocket('ws://localhost:__PORT__/ws');
ws.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    if (msg.type === 'scroll') {
        if (!_els.length) return;
        const line = msg.line;
        let prev = _els[0], next = null;
        for (let i = 0; i < _els.length; i++) {
            if (_els[i].line <= line) { prev = _els[i]; next = _els[i+1] || null; }
            else break;
        }
        let targetTop;
        if (next && next.line > prev.line) {
            const frac = (line - prev.line) / (next.line - prev.line);
            targetTop = prev.top + frac * (next.top - prev.top);
        } else {
            targetTop = prev.top;
        }
        window.scrollTo({ top: targetTop - window.innerHeight * 0.5, behavior: 'smooth' });
    }
    if (msg.type === 'reload') {
        fetch('/').then(r => r.text()).then(html => {
            const doc = new DOMParser().parseFromString(html, 'text/html');
            document.querySelector('#content').innerHTML =
                doc.querySelector('#content').innerHTML;
            hljs.highlightAll();
            mdpRenderMath();
            // Drive pandoc.wasm over any latex-pending blocks the
            // refreshed body introduced; harmless no-op when none exist
            // or when the renderer module hasn't loaded yet.
            if (typeof window.mdpRenderLatex === 'function') {
                window.mdpRenderLatex(document);
            }
            cacheEls();
        });
    }
};
ws.onclose = () => {
    // When the server exits (mdp watch Ctrl-C, or the nvim plugin shutting
    // down) the WS closes. window.close() works for chrome --app= popups;
    // for regular tabs (xdg-open / firefox --new-window) browsers block the
    // call silently. We replace the body either way so it's obvious the
    // preview is dead — the nvim plugin's xdotool/wmctrl fallback can still
    // kill the tab from outside.
    try { window.close(); } catch (_) {}
    setTimeout(() => {
        document.body.innerHTML =
            '<p style="font-family:sans-serif;padding:2rem;opacity:0.5">' +
            'md-preview server stopped — you can close this tab.</p>';
    }, 50);
};
`

// BuildPage wraps an HTML body in the preview page template using
// /_/ as the LaTeX-assets URL prefix (server mode).
func BuildPage(body, theme string, wsPort int, extraCSS string, colemak bool) string {
	return BuildPageWithAssets(body, theme, wsPort, extraCSS, colemak, "/_/")
}

// BuildPageWithAssets is BuildPage with a caller-provided URL prefix
// for the LaTeX bundle (pandoc.wasm.gz, pandoc.js, etc). Use "/_/"
// for HTTP-served previews; for static file:// previews pass an
// absolute file:// URL pointing at the sibling-assets dir from
// latex.WriteSiblingAssets.
func BuildPageWithAssets(body, theme string, wsPort int, extraCSS string, colemak bool, assetsPrefix string) string {
	cssVars := CSSDark
	hljsThemeCSS := hljsThemeDarkCSS
	if theme == "light" {
		cssVars = CSSLight
		hljsThemeCSS = hljsThemeLightCSS
	}

	wsScript := ""
	if wsPort > 0 {
		wsScript = strings.ReplaceAll(wsScriptTemplate, "__PORT__", fmt.Sprintf("%d", wsPort))
	}

	// Skip the ~645 KiB KaTeX bundle when the body has no math markers.
	// The vast majority of markdown previews are math-free and shouldn't
	// pay that cost. mdpRenderMath() is still defined unconditionally
	// (it no-ops when renderMathInElement is undefined).
	katexCSSOut, katexJSOut, katexAutoRenderJSOut := "", "", ""
	if hasMath(body) {
		katexCSSOut = katexCSS
		katexJSOut = katexScript
		katexAutoRenderJSOut = katexAutoRenderScript
	}

	// Bundle pandoc.wasm + glue scripts only when the rendered body
	// has at least one latex-pending placeholder. pandoc.wasm is
	// ~58 MB raw / ~15 MB gzipped; pulling it in for every preview
	// would inflate first-paint time for the math-free common case.
	// The CSS is appended unconditionally — a few hundred bytes — so
	// .latex-block / .latex-error / .latex-pending styles are
	// available even if the user toggles content via WS reload.
	latexScripts := ""
	if latex.HasLatex(body) {
		latexScripts = fmt.Sprintf(latexScriptTags, assetsPrefix, assetsPrefix)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
%s
%s
%s
%s
%s
%s
.katex { color: var(--color-text-primary); }
</style>
</head>
<body>
<div id="content" class="markdown-body">
%s
</div>
<script>
%s
hljs.highlightAll();
%s
%s
function mdpRenderMath() {
  if (typeof renderMathInElement !== "function") return;
  renderMathInElement(document.querySelector("#content"), {
    delimiters: [
      {left: "$$", right: "$$", display: true},
      {left: "\\[", right: "\\]", display: true},
      {left: "\\(", right: "\\)", display: false}
    ],
    throwOnError: false
  });
}
window.mdpRenderMath = mdpRenderMath;
mdpRenderMath();
%s
%s
</script>
%s
</body>
</html>`, hljsThemeCSS, cssVars, CSSCommon, latexCSS, katexCSSOut, extraCSS, body, hljsScript, katexJSOut, katexAutoRenderJSOut, vimKeys(colemak), wsScript, latexScripts)
}

// latexCSS styles the three states the placeholder cycles through:
// pending (server emitted, WASM hasn't rendered yet), block (success),
// error (pandoc-wasm threw). Bundled unconditionally since it's tiny.
const latexCSS = `
.markdown-body .latex-pending {
  color: var(--color-text-secondary);
  font-style: italic;
  padding: 1em;
  border: 1px dashed var(--color-border);
  border-radius: 6px;
  margin-bottom: 1em;
}
.markdown-body .latex-block { margin-bottom: 1em; }
.markdown-body .latex-error {
  color: var(--color-alert-caution);
  background: var(--color-bg-code);
  padding: 8px 12px;
  border-left: 4px solid var(--color-alert-caution);
  border-radius: 4px;
  font-family: "SFMono-Regular", Consolas, monospace;
  font-size: 90%;
  margin-bottom: 1em;
  white-space: pre-wrap;
}
`

// %s twice: gets the assets URL prefix. purify.min.js must load
// synchronously (no defer) so window.DOMPurify is set before the
// module's top-level await fires.
const latexScriptTags = `<script src="%spurify.min.js"></script>
<script type="module" src="%slatex-render.js"></script>
`

// hasMath checks whether the rendered body has any math markers worth
// loading KaTeX for. Detection is cheap (substring scan) and only needs
// to be correct enough to skip the bundle when truly math-free; a false
// positive just pays the bundle cost we'd already accept by default.
//
// Single-dollar delimiters are intentionally NOT detected: KaTeX
// auto-render isn't configured for them (prose like `Costs $5 and $10`
// produced too many false positives), so a body whose only math marker
// is a bare `$` won't render either way.
//
// A latex-pending placeholder triggers the bundle too: pandoc.wasm's
// --mathjax output stamps the same \(...\) / \[...\] markers KaTeX's
// auto-render scans for, and latex-render.js re-runs mdpRenderMath()
// over the swapped-in HTML.
func hasMath(body string) bool {
	for _, marker := range []string{
		`\(`, `\[`, `$$`,
		`class="math inline"`, `class="math display"`,
		`class="latex-pending"`,
	} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

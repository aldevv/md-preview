package render

import (
	"fmt"
	"strings"
)

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

// BuildPage wraps an HTML body in the preview page template.
//
// theme selects the color palette: "dark" (default) or "light".
// wsPort > 0 embeds the WebSocket scroll/reload client.
// extraCSS is appended after the default CSS so it wins via cascade.
// colemak swaps the in-page nav keys from j/k/l to n/e/i.
func BuildPage(body, theme string, wsPort int, extraCSS string, colemak bool) string {
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
	katexCSSOut, katexJSOut, katexAutoRenderJSOut := "", "", ""
	if hasMath(body) {
		katexCSSOut = katexCSS
		katexJSOut = katexScript
		katexAutoRenderJSOut = katexAutoRenderScript
	}
	// Skip the ~3.3 MiB mermaid bundle when no mermaid fence is present.
	mermaidJSOut, mermaidInit := "", ""
	if hasMermaid(body) {
		mermaidJSOut = mermaidScript
		mermaidInit = `mermaid.initialize({startOnLoad:true,theme:'` + mermaidTheme(theme) + `'});`
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
%s
%s
</script>
</body>
</html>`, hljsThemeCSS, cssVars, CSSCommon, pandocCSS, katexCSSOut, extraCSS, body, hljsScript, katexJSOut, katexAutoRenderJSOut, mermaidJSOut, mermaidInit, vimKeys(colemak), wsScript)
}

// hasMermaid reports whether the rendered body contains a mermaid
// fence (emitMermaidFence stamps class="mermaid"). Used by BuildPage
// to skip the ~3.3 MiB mermaid bundle for pages with no diagrams.
func hasMermaid(body string) bool {
	return strings.Contains(body, `class="mermaid"`)
}

// mermaidTheme maps mdp's theme name to a mermaid built-in theme so
// rendered diagrams visually match the surrounding page chrome.
func mermaidTheme(theme string) string {
	if theme == "light" {
		return "default"
	}
	return "dark"
}

// hasMath checks whether the rendered body has any math markers worth
// loading KaTeX for. Single-dollar delimiters are intentionally NOT
// detected: KaTeX auto-render isn't configured for them (prose like
// `Costs $5 and $10` produced too many false positives).
func hasMath(body string) bool {
	for _, marker := range []string{
		`\(`, `\[`, `$$`,
		`class="math inline"`, `class="math display"`,
	} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

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
// __RELOAD_CASE__ becomes the `r` reload binding in static mode and is
// stripped in WS-backed modes (watch/serve), which drive their own
// content refresh.
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
            __RELOAD_CASE__
            default: return;
        }
        e.preventDefault();
    });
})();
`

// vimKeys returns the navigation script with j/k/l (default) or n/e/i
// (colemak) substituted in. When staticReload is true, pressing `r`
// triggers location.reload() so the user can pull in changes after
// re-running `mdp <file>`; WS-backed modes leave it out.
func vimKeys(colemak, staticReload bool) string {
	down, up, right := "j", "k", "l"
	if colemak {
		down, up, right = "n", "e", "i"
	}
	s := strings.ReplaceAll(vimKeysScriptTemplate, "__DOWN__", down)
	s = strings.ReplaceAll(s, "__UP__", up)
	s = strings.ReplaceAll(s, "__RIGHT__", right)
	reloadCase := ""
	if staticReload {
		reloadCase = "case 'r': location.reload(); break;"
	}
	s = strings.ReplaceAll(s, "__RELOAD_CASE__", reloadCase)
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
        // The reload broadcast carries the absolute path of whatever
        // file the server just rendered. Track it so the click
        // handler resolves relative hrefs against the new dir.
        if (msg.file) {
            window.mdpCurrentFile = msg.file;
        }
        fetch('/').then(r => r.text()).then(html => {
            const doc = new DOMParser().parseFromString(html, 'text/html');
            document.querySelector('#content').innerHTML =
                doc.querySelector('#content').innerHTML;
            hljs.highlightAll();
            mdpRenderMath();
            cacheEls();
        });
    }
};
// Anchor the initial history entry to the current file so popstate
// after one or more navigations can restore the right document.
if (window.mdpCurrentFile && history.state == null) {
    history.replaceState({mdpFile: window.mdpCurrentFile}, '', '');
}

// popstate fires on back/forward. Re-render whatever file the
// previous entry pointed at; the reload broadcast then swaps the
// page content. If the state has no mdpFile (i.e. we ended up at
// pre-mdp history), do nothing and let the browser navigate away.
window.addEventListener('popstate', (e) => {
    if (e.state && e.state.mdpFile) {
        const tgt = e.state.mdpFile;
        // Slide mdpIdx along the existing stack so the buttons
        // reflect where we are. Match against neighbouring entries
        // (one back or one forward); fall back to a search.
        if (mdpIdx >= 1 && mdpStack[mdpIdx - 1] === tgt) {
            mdpIdx--;
        } else if (mdpIdx >= 0 && mdpStack[mdpIdx + 1] === tgt) {
            mdpIdx++;
        } else {
            const found = mdpStack.indexOf(tgt);
            if (found >= 0) mdpIdx = found;
        }
        mdpSaveNav();
        fetch('/render', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({file: tgt})
        });
    }
});

// Click-to-navigate: any <a> inside #content whose href is a local
// path POSTs to /render. The server's reload broadcast then swaps
// the page content; on 4xx the server's error message is surfaced
// via mdpShowToast and the page doesn't change.
document.getElementById('content').addEventListener('click', (e) => {
    const a = e.target.closest('a');
    if (!a) return;
    const href = a.getAttribute('href');
    if (!href) return;
    // Skip anchor links (browser handles) and external schemes.
    if (href.startsWith('#') || /^[a-z][a-z0-9+.-]*:/i.test(href)) return;
    e.preventDefault();
    // Resolve relative hrefs against the current file's directory.
    let target = href;
    if (!target.startsWith('/') && window.mdpCurrentFile) {
        const dir = window.mdpCurrentFile.replace(/\/[^/]*$/, '');
        target = dir + '/' + href;
    }
    // Normalize . and .. segments.
    const parts = target.split('/');
    const out = [];
    for (const seg of parts) {
        if (seg === '..') out.pop();
        else if (seg !== '.' && seg !== '') out.push(seg);
    }
    target = (target.startsWith('/') ? '/' : '') + out.join('/');
    fetch('/render', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({file: target})
    }).then(async (r) => {
        if (r.ok) {
            // Push a new entry so the browser back button (and our
            // back chrome button) returns to the previous file.
            history.pushState({mdpFile: target}, '', '');
            mdpNavigatedTo(target);
            return;
        }
        let msg = 'navigation failed (' + r.status + ')';
        try {
            const data = await r.json();
            if (data && data.error) msg = data.error;
        } catch (_) {}
        mdpShowToast(msg);
    }).catch((err) => {
        mdpShowToast('navigation failed: ' + err);
    });
});

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
// currentFile is the absolute path of the document being rendered,
// exposed to the click handler so it can resolve relative hrefs
// against its directory. Empty when no file context is meaningful
// (e.g. ad-hoc RenderBytes callers).
func BuildPage(body, theme string, wsPort int, extraCSS string, colemak bool, currentFile string) string {
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
	// Embed the current file path as a JS string literal so the
	// click-to-navigate handler can resolve relative hrefs. Empty
	// values (RenderBytes, tests) get an empty string; the handler
	// no-ops when it's empty. fmt.Sprintf("%q", s) is Go-quoting,
	// valid JS (handles backslashes, quotes, and unicode escapes).
	currentFileJS := fmt.Sprintf("%q", currentFile)

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
%s
.katex { color: var(--color-text-primary); }
</style>
</head>
<body>
<button id="mdp-back" class="mdp-nav-btn" aria-label="Back" title="Back" hidden>&#8249;</button>
<button id="mdp-fwd" class="mdp-nav-btn" aria-label="Forward" title="Forward" hidden>&#8250;</button>
<div id="content" class="markdown-body">
%s
</div>
<div id="mdp-toast" hidden></div>
<script>
window.mdpCurrentFile = %s;
// mdpStack + mdpIdx track the user's nav path so back/forward
// buttons reflect actual nav state (not browser history length,
// which includes pre-mdp entries). sessionStorage persists across
// real page loads in static mode; in WS mode the page never
// reloads but stays consistent with the same data.
let mdpStack = JSON.parse(sessionStorage.getItem('mdpStack') || '[]');
let mdpIdx = parseInt(sessionStorage.getItem('mdpIdx') || '-1', 10);
const mdpBackBtn = document.getElementById('mdp-back');
const mdpFwdBtn = document.getElementById('mdp-fwd');
function mdpSaveNav() {
  sessionStorage.setItem('mdpStack', JSON.stringify(mdpStack));
  sessionStorage.setItem('mdpIdx', String(mdpIdx));
  mdpUpdateNavButtons();
}
function mdpUpdateNavButtons() {
  if (mdpBackBtn) mdpBackBtn.hidden = mdpIdx <= 0;
  if (mdpFwdBtn)  mdpFwdBtn.hidden  = mdpIdx >= mdpStack.length - 1;
}
function mdpNavigatedTo(target) {
  // A new navigation (not back/forward): discard forward history,
  // append, advance idx.
  if (mdpIdx >= 0 && mdpStack[mdpIdx] === target) return;
  mdpStack = mdpStack.slice(0, mdpIdx + 1);
  mdpStack.push(target);
  mdpIdx = mdpStack.length - 1;
  mdpSaveNav();
}
// Sync stack with the file we ended up on (covers static-mode
// back/forward navigations between separate file:// pages).
(function () {
  const cur = window.mdpCurrentFile;
  if (!cur) return;
  if (mdpIdx >= 0 && mdpStack[mdpIdx] === cur) {
    // in sync
  } else if (mdpIdx >= 1 && mdpStack[mdpIdx - 1] === cur) {
    mdpIdx--;
  } else if (mdpIdx >= 0 && mdpStack[mdpIdx + 1] === cur) {
    mdpIdx++;
  } else {
    mdpStack = mdpStack.slice(0, mdpIdx + 1);
    mdpStack.push(cur);
    mdpIdx = mdpStack.length - 1;
  }
  mdpSaveNav();
})();
if (mdpBackBtn) mdpBackBtn.addEventListener('click', () => window.history.back());
if (mdpFwdBtn)  mdpFwdBtn.addEventListener('click',  () => window.history.forward());
function mdpShowToast(msg) {
  const el = document.getElementById('mdp-toast');
  if (!el) return;
  el.textContent = msg;
  el.hidden = false;
  // Force reflow so the transition kicks in.
  void el.offsetWidth;
  el.classList.add('visible');
  clearTimeout(el._mdpTimer);
  el._mdpTimer = setTimeout(() => {
    el.classList.remove('visible');
    setTimeout(() => { el.hidden = true; }, 250);
  }, 3000);
}
window.mdpShowToast = mdpShowToast;
// mdpStaticToast is the target of javascript:... hrefs that static
// mode emits for links it can't honour (out-of-tree, missing,
// unsupported, over-cap). The payload is URI-encoded; decode and
// hand off to mdpShowToast.
function mdpStaticToast(encoded) {
  mdpShowToast(decodeURIComponent(encoded));
}
window.mdpStaticToast = mdpStaticToast;
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
</html>`, hljsThemeCSS, cssVars, CSSCommon, pandocCSS, chromeCSS, katexCSSOut, extraCSS, body, currentFileJS, hljsScript, katexJSOut, katexAutoRenderJSOut, mermaidJSOut, mermaidInit, vimKeys(colemak, wsPort == 0), wsScript)
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

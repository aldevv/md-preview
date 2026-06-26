package page

import (
	"fmt"
	"html"
	"path/filepath"
	"sort"
	"strings"
)

// __KEYS_JSON__ is replaced with the resolved key map. __RELOAD_CONDITION__
// becomes the reload predicate in static mode and false in WS-backed modes,
// which drive their own refresh.
const vimKeysScriptTemplate = `
(() => {
    const STEP = 60;
    const KEYS = __KEYS_JSON__;
    const STATIC_RELOAD = __RELOAD_STATIC__;
    // Multi-char bindings that aren't named-keys (Tab, Enter, ArrowUp, ...)
    // are treated as 2+ keypress sequences. SEQ_TIMEOUT_MS bounds how long
    // we wait after a prefix key before firing its single-key action.
    const SPECIAL_KEYS = new Set(['Tab','Enter','Escape','Backspace','Delete','Home','End','PageUp','PageDown','ArrowUp','ArrowDown','ArrowLeft','ArrowRight','F1','F2','F3','F4','F5','F6','F7','F8','F9','F10','F11','F12']);
    function isSequenceBinding(v) {
        if (!v || v.length < 2) return false;
        if (v.startsWith('Ctrl+') || v.startsWith('Meta+') || v.startsWith('Alt+') || v.startsWith('Shift+')) return false;
        return !SPECIAL_KEYS.has(v);
    }
    const SEQ_MAP = {};
    const SEQ_PREFIXES = new Set();
    const SEQ_TIMEOUT_MS = 400;
    let pendingPrefix = null;
    let pendingTimer = null;
    function clearPending() {
        if (pendingTimer) { clearTimeout(pendingTimer); pendingTimer = null; }
        pendingPrefix = null;
    }
    function key(action) { return KEYS[action] || ''; }
    function isKey(e, action) { return key(action) && e.key === key(action); }
    const PAGE_ACTIONS = ['down','up','left','right','half_down','half_up','full_down','full_up','top','bottom','history_back','history_forward','zoom_in','zoom_out','zoom_reset','close','toc_open','reload'];
    for (const action of PAGE_ACTIONS) {
        const v = KEYS[action];
        if (isSequenceBinding(v)) {
            SEQ_MAP[v] = action;
            SEQ_PREFIXES.add(v[0]);
        }
    }
    function actionForKey(k) {
        if (!k) return null;
        for (const a of PAGE_ACTIONS) if (KEYS[a] === k) return a;
        return null;
    }
    function isEditable(el) {
        if (!el) return false;
        const tag = el.tagName;
        return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable;
    }
    function overlayBlock() {
        return !!(window.mdpTreeIsOpen || window.mdpTocIsOpen || window.mdpFinderIsOpen || window.mdpSelectIsActive || window.mdpSearchIsOpen);
    }
    const ZOOM_MIN = 0.3, ZOOM_MAX = 3.0, ZOOM_STEP = 0.1;
    function mdpReadZoom() {
        const v = parseFloat(sessionStorage.getItem('mdpZoom'));
        return isFinite(v) && v > 0 ? v : 1;
    }
    function mdpApplyZoom(z) {
        z = Math.max(ZOOM_MIN, Math.min(ZOOM_MAX, z));
        document.body.style.zoom = z;
        sessionStorage.setItem('mdpZoom', String(z));
    }
    mdpApplyZoom(mdpReadZoom());
    // Hold-to-scroll: a single d/u/f/b press is a smooth half- or
    // full-page jump. Holding switches to a rAF-driven constant-
    // velocity scroll. Each call sets its own pxPerFrame so f/b hold
    // is roughly twice as fast as d/u hold, matching their single-
    // press semantics. behavior:'auto' per step keeps the motion
    // linear (no queued smooth animations).
    const mdpHoldHalfPx = 28;
    const mdpHoldFullPx = 56;
    let mdpHoldDir = 0;
    let mdpHoldPx = mdpHoldHalfPx;
    let mdpHoldRAF = null;
    function mdpHoldTick() {
        if (!mdpHoldDir) return;
        window.scrollBy({ top: mdpHoldDir * mdpHoldPx, behavior: 'auto' });
        mdpHoldRAF = requestAnimationFrame(mdpHoldTick);
    }
    function mdpStartHold(dir, pxPerFrame) {
        if (mdpHoldDir === dir && mdpHoldPx === pxPerFrame) return;
        mdpStopHold();
        // Cancel any in-flight smooth scroll from the single-press
        // handler so it doesn't interleave with the rAF-driven steps.
        window.scrollTo({ top: window.scrollY, behavior: 'auto' });
        mdpHoldDir = dir;
        mdpHoldPx = pxPerFrame;
        mdpHoldRAF = requestAnimationFrame(mdpHoldTick);
    }
    function mdpStopHold() {
        mdpHoldDir = 0;
        if (mdpHoldRAF) { cancelAnimationFrame(mdpHoldRAF); mdpHoldRAF = null; }
    }
    // dispatchSingle runs the single-key body for an action. e may be
    // null when the action fires retroactively (sequence-timeout flush);
    // hold-scroll keys gracefully degrade to their one-shot smooth scroll.
    function dispatchSingle(action, e) {
        const h = window.innerHeight;
        switch (action) {
            case 'down':  window.scrollBy({ top:  STEP, behavior: 'auto' }); return true;
            case 'up':    window.scrollBy({ top: -STEP, behavior: 'auto' }); return true;
            case 'left':  window.scrollBy({ left: -STEP, behavior: 'auto' }); return true;
            case 'right': window.scrollBy({ left:  STEP, behavior: 'auto' }); return true;
            case 'half_down':
                if (e && e.repeat) mdpStartHold(1, mdpHoldHalfPx);
                else window.scrollBy({ top:  h / 2, behavior: 'smooth' });
                return true;
            case 'half_up':
                if (e && e.repeat) mdpStartHold(-1, mdpHoldHalfPx);
                else window.scrollBy({ top: -h / 2, behavior: 'smooth' });
                return true;
            case 'full_down':
                if (e && e.repeat) mdpStartHold(1, mdpHoldFullPx);
                else window.scrollBy({ top:  h, behavior: 'smooth' });
                return true;
            case 'full_up':
                if (e && e.repeat) mdpStartHold(-1, mdpHoldFullPx);
                else window.scrollBy({ top: -h, behavior: 'smooth' });
                return true;
            case 'top':    window.scrollTo({ top: 0, behavior: 'smooth' }); return true;
            case 'bottom': window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'smooth' }); return true;
            case 'history_back':    if (typeof mdpGoBack === 'function') { mdpGoBack(); return true; } return false;
            case 'history_forward': if (typeof mdpGoForward === 'function') { mdpGoForward(); return true; } return false;
            case 'zoom_in':    mdpApplyZoom(mdpReadZoom() + ZOOM_STEP); return true;
            case 'zoom_out':   mdpApplyZoom(mdpReadZoom() - ZOOM_STEP); return true;
            case 'zoom_reset': mdpApplyZoom(1); return true;
            case 'close':      window.close(); return true;
            case 'toc_open':   if (typeof mdpToggleToc === 'function') { mdpToggleToc(); return true; } return false;
            case 'reload':
                if (STATIC_RELOAD) { location.reload(); return true; }
                fetch('/render', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: '{}'}).catch(() => {});
                return true;
        }
        return false;
    }
    document.addEventListener('keydown', (e) => {
        if (e.ctrlKey || e.metaKey || e.altKey) { clearPending(); return; }
        if (isEditable(e.target)) { clearPending(); return; }

        // Sequence completion: a pending prefix is in flight. Sequences
        // fire even when an overlay is open so toc_open (and friends)
        // can toggle from inside.
        if (pendingPrefix) {
            const seq = pendingPrefix + e.key;
            if (SEQ_MAP[seq]) {
                e.preventDefault();
                const action = SEQ_MAP[seq];
                clearPending();
                dispatchSingle(action, e);
                return;
            }
            const prefAction = actionForKey(pendingPrefix);
            clearPending();
            if (prefAction && !overlayBlock()) dispatchSingle(prefAction, null);
            // Fall through so the new key gets a fresh dispatch.
        }

        if (overlayBlock()) {
            // Still allow starting a sequence (so a fresh gO from inside
            // an overlay can toggle it shut), but never run single-key
            // actions when an overlay owns the keys.
            if (SEQ_PREFIXES.has(e.key)) {
                pendingPrefix = e.key;
                pendingTimer = setTimeout(clearPending, SEQ_TIMEOUT_MS);
                e.preventDefault();
            }
            return;
        }

        if (SEQ_PREFIXES.has(e.key)) {
            const k = e.key;
            pendingPrefix = k;
            pendingTimer = setTimeout(() => {
                pendingPrefix = null;
                pendingTimer = null;
                const action = actionForKey(k);
                if (action && !overlayBlock()) dispatchSingle(action, null);
            }, SEQ_TIMEOUT_MS);
            e.preventDefault();
            return;
        }

        const action = actionForKey(e.key);
        if (action && dispatchSingle(action, e)) e.preventDefault();
    });
    document.addEventListener('keyup', (e) => {
        if (isKey(e, 'half_down') || isKey(e, 'half_up') || isKey(e, 'full_down') || isKey(e, 'full_up')) mdpStopHold();
    });
    window.addEventListener('blur', mdpStopHold);
})();
`

type KeyBindings map[string]string

func defaultKeyBindings(colemak bool) KeyBindings {
	down, up, right := "j", "k", "l"
	forward := "L"
	searchNext, searchPrev := "n", "N"
	if colemak {
		down, up, right = "n", "e", "i"
		forward = "I"
		searchNext, searchPrev = "k", "K"
	}
	return KeyBindings{
		"down":                down,
		"up":                  up,
		"left":                "h",
		"right":               right,
		"half_down":           "d",
		"half_up":             "u",
		"full_down":           "f",
		"full_up":             "b",
		"top":                 "g",
		"bottom":              "G",
		"history_back":        "H",
		"history_forward":     forward,
		"close":               "q",
		"reload":              "r",
		"toc_open":            "gO",
		"tree_toggle":         "Tab",
		"tree_down":           down,
		"tree_up":             up,
		"tree_left":           "h",
		"tree_right":          right,
		"tree_open":           "Enter",
		"finder_open":         "Ctrl+p",
		"search_open":         "/",
		"search_next":         searchNext,
		"search_prev":         searchPrev,
		"select_pick":         "s",
		"select_visual":       "v",
		"select_left":         "h",
		"select_right":        right,
		"select_down":         down,
		"select_up":           up,
		"select_word_next":    "w",
		"select_word_prev":    "b",
		"select_line_start":   "0",
		"select_line_end":     "$",
		"select_top":          "g",
		"select_bottom":       "G",
		"select_yank":         "y",
		"select_toggle_lines": "L",
		"select_ask":          "c",
		"ask_again":           "a",
		"zoom_in":             "+",
		"zoom_out":            "-",
		"zoom_reset":          "0",
	}
}

func resolveKeyBindings(colemak bool, overrides map[string]string) KeyBindings {
	keys := defaultKeyBindings(colemak)
	for action, key := range overrides {
		if _, ok := keys[action]; ok {
			keys[action] = key
		}
	}
	return keys
}

func keysJSON(keys KeyBindings) string {
	var b strings.Builder
	b.WriteByte('{')
	actions := make([]string, 0, len(keys))
	for action := range keys {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for i, action := range actions {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q:%q", action, keys[action])
	}
	b.WriteByte('}')
	return b.String()
}

func vimKeys(keys KeyBindings, staticReload bool) string {
	s := strings.ReplaceAll(vimKeysScriptTemplate, "__KEYS_JSON__", keysJSON(keys))
	reloadStatic := "false"
	if staticReload {
		reloadStatic = "true"
	}
	s = strings.ReplaceAll(s, "__RELOAD_STATIC__", reloadStatic)
	return s
}

// sharedNavScriptTemplate carries the data + navigation primitives that
// both the Tab sidebar and the Ctrl+P fuzzy finder consume. Emitted whenever
// either UI is on. __STATIC_TREE_DATA__ holds the "window.mdpStaticTree =
// ...;" injection in static mode; empty in WS mode (the page fetches
// /tree on first open).
const sharedNavScriptTemplate = `
__STATIC_TREE_DATA__
let mdpTreeData = null;

async function mdpEnsureTreeData() {
  if (mdpTreeData) return mdpTreeData;
  if (window.mdpStaticTree) { mdpTreeData = window.mdpStaticTree; return mdpTreeData; }
  try {
    const r = await fetch('/tree');
    if (!r.ok) throw new Error('tree fetch failed (' + r.status + ')');
    mdpTreeData = await r.json();
  } catch (e) {
    mdpShowToast('tree unavailable: ' + e.message);
    return null;
  }
  return mdpTreeData;
}

function mdpCurrentRel(data) {
  if (!data || !window.mdpCurrentFile) return '';
  const root = data.root;
  if (window.mdpCurrentFile === root) return '';
  if (!window.mdpCurrentFile.startsWith(root + '/')) return '';
  return window.mdpCurrentFile.slice(root.length + 1);
}

// keepOpen=true skips the tree close in WS mode and sets a sessionStorage
// flag so the next static-mode page reopens the tree on load.
function mdpTreeNavigate(rel, opts) {
  const data = mdpTreeData;
  if (!data) return;
  const keepOpen = !!(opts && opts.keepOpen);
  const target = data.root + '/' + rel;
  if (data.rendered) {
    const tmp = data.rendered[rel];
    if (!tmp) { mdpShowToast('not pre-rendered: ' + rel); return; }
    if (keepOpen) sessionStorage.setItem('mdpTreeAutoOpen', '1');
    window.location.href = tmp;
    return;
  }
  fetch('/render', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({file: target})
  }).then(async (r) => {
    if (r.ok) {
      history.pushState({mdpFile: target}, '', '');
      mdpNavigatedTo(target);
      if (!keepOpen && typeof mdpToggleTree === 'function') mdpToggleTree(false);
      return;
    }
    let msg = 'navigation failed (' + r.status + ')';
    try { const d = await r.json(); if (d && d.error) msg = d.error; } catch (_) {}
    mdpShowToast(msg);
  }).catch((err) => { mdpShowToast('navigation failed: ' + err); });
}
`

// treeScriptTemplate powers the Tab-toggle file sidebar. Depends on
// sharedNavScriptTemplate (mdpEnsureTreeData, mdpTreeNavigate); always
// emitted together.
// sharedNavScriptTemplate (mdpEnsureTreeData, mdpTreeNavigate).

// __PORT__ is replaced with the server port at runtime.
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
            document.title = mdpFormatTitle(msg.file);
            // Per-file history: drop the cache so the next Shift+Tab
            // fetches answers for the file we just switched to.
            if (window.mdpSelectState) {
                window.mdpSelectState.askHistoryCache = null;
                window.mdpSelectState.askHistoryIdx = -1;
            }
        }
        fetch('/').then(r => r.text()).then(html => {
            const doc = new DOMParser().parseFromString(html, 'text/html');
            document.querySelector('#content').innerHTML =
                doc.querySelector('#content').innerHTML;
            if (typeof hljs !== 'undefined') hljs.highlightAll();
            mdpRenderMath();
            if (typeof mdpAttachCopyButtons === 'function') mdpAttachCopyButtons();
            cacheEls();
            if (typeof window.mdpSearchClear === 'function') window.mdpSearchClear();
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
    if (href.startsWith('#') || /^[a-z][a-z0-9+.-]*:/i.test(href)) return;
    e.preventDefault();
    let target = href;
    if (!target.startsWith('/') && window.mdpCurrentFile) {
        const dir = window.mdpCurrentFile.replace(/\/[^/]*$/, '');
        target = dir + '/' + href;
    }
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
    // window.close() works for chrome --app= popups; regular tabs
    // (xdg-open, firefox --new-window) silently ignore it. The body
    // replacement is the visible fallback; the nvim plugin's
    // xdotool/wmctrl path can still close the tab from outside.
    try { window.close(); } catch (_) {}
    setTimeout(() => {
        document.body.innerHTML =
            '<p style="font-family:sans-serif;padding:2rem;opacity:0.5">' +
            'md-preview server stopped, you can close this tab.</p>';
    }, 50);
};
`

// pageTemplate is the preview page body. Placeholders match the
// __TOKEN__ style used by vimKeysScriptTemplate / wsScriptTemplate
// and are filled by strings.NewReplacer in BuildPage. Named
// placeholders avoid the positional-arg fragility that a 17-arg
// fmt.Sprintf would carry.
const pageTemplate = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__TITLE__</title>
<script>
// Page-side timing markers. Visible in DevTools (Ctrl+Shift+I when
// MDP_DEBUG=1) and surfaced into document.title prefix in debug mode.
window.__mdpT0 = performance.now();
window.__mdpMark = function (label) {
  var t = (performance.now() - window.__mdpT0).toFixed(1);
  console.log('[mdp-time] page: ' + label + ' ' + t + 'ms');
};
window.__mdpMark('parse start');
document.addEventListener('readystatechange', function () {
  window.__mdpMark('readyState=' + document.readyState);
});
document.addEventListener('DOMContentLoaded', function () {
  window.__mdpMark('DOMContentLoaded');
});
window.addEventListener('load', function () {
  window.__mdpMark('window load');
});
if (typeof PerformanceObserver === 'function') {
  try {
    new PerformanceObserver(function (list) {
      for (var entry of list.getEntries()) {
        window.__mdpMark(entry.name + ' (paint)');
      }
    }).observe({ type: 'paint', buffered: true });
  } catch (_) {}
}
</script>
<style>
__HLJS_THEME_CSS__
__CSS_VARS__
__CSS_COMMON__
__PANDOC_CSS__
__CHROME_CSS__
__KATEX_CSS__
__EXTRA_CSS__
.katex { color: var(--color-text-primary); }
</style>
</head>
<body>
<div id="mdp-nav-bar" hidden>
<button id="mdp-back" class="mdp-nav-btn" aria-label="Back" title="Back" hidden>&#8249;</button>
<button id="mdp-fwd" class="mdp-nav-btn" aria-label="Forward" title="Forward" hidden>&#8250;</button>
</div>
__TREE_DOM__
__TOC_DOM__
__FINDER_DOM__
__SEARCH_DOM__
<div id="content" class="markdown-body">
__BODY__
</div>
<div id="mdp-toast" hidden></div>
<div id="mdp-install-toast" hidden>Install Chromium-based browser for independent windows</div>
<script>
window.mdpCurrentFile = __CURRENT_FILE_JS__;
function mdpMatchesKeySpec(e, spec) {
  if (!spec) return false;
  const parts = spec.split('+').map((p) => p.trim()).filter(Boolean);
  if (!parts.length) return false;
  const key = parts.pop();
  const mods = new Set(parts.map((p) => p.toLowerCase()));
  if (e.ctrlKey !== mods.has('ctrl')) return false;
  if (e.metaKey !== mods.has('meta')) return false;
  if (e.altKey !== mods.has('alt')) return false;
  if (e.shiftKey !== mods.has('shift')) return false;
  if (key.length === 1) return e.key.toLowerCase() === key.toLowerCase();
  return e.key === key;
}
function mdpFormatTitle(path) {
  if (!path) return 'md-preview';
  const parts = path.split('/').filter(Boolean);
  const base = parts[parts.length - 1] || path;
  const parent = parts.length >= 2 ? parts[parts.length - 2] : '';
  return parent ? parent + '/' + base : base;
}
window.mdpFormatTitle = mdpFormatTitle;
// mdpStack + mdpIdx track the user's nav path so back/forward
// buttons reflect actual nav state (not browser history length,
// which includes pre-mdp entries). sessionStorage persists across
// real page loads in static mode; in WS mode the page never
// reloads but stays consistent with the same data.
let mdpStack = JSON.parse(sessionStorage.getItem('mdpStack') || '[]');
let mdpIdx = parseInt(sessionStorage.getItem('mdpIdx') || '-1', 10);
let mdpURLs = JSON.parse(sessionStorage.getItem('mdpURLs') || '[]');
// Backfill (or trim) URLs to match the path stack so per-index lookups stay aligned.
while (mdpURLs.length < mdpStack.length) mdpURLs.push('');
mdpURLs.length = mdpStack.length;
const mdpBackBtn = document.getElementById('mdp-back');
const mdpFwdBtn = document.getElementById('mdp-fwd');
function mdpSaveNav() {
  sessionStorage.setItem('mdpStack', JSON.stringify(mdpStack));
  sessionStorage.setItem('mdpIdx', String(mdpIdx));
  sessionStorage.setItem('mdpURLs', JSON.stringify(mdpURLs));
  mdpUpdateNavButtons();
}
function mdpUpdateNavButtons() {
  const canBack = mdpIdx > 0;
  const canFwd  = mdpIdx >= 0 && mdpIdx < mdpStack.length - 1;
  if (mdpBackBtn) mdpBackBtn.hidden = !canBack;
  if (mdpFwdBtn)  mdpFwdBtn.hidden  = !canFwd;
  const bar = document.getElementById('mdp-nav-bar');
  if (bar) bar.hidden = !canBack && !canFwd;
}
function mdpNavigatedTo(target) {
  // A new navigation (not back/forward): discard forward history,
  // append, advance idx.
  if (mdpIdx >= 0 && mdpStack[mdpIdx] === target) return;
  mdpStack = mdpStack.slice(0, mdpIdx + 1);
  mdpURLs = mdpURLs.slice(0, mdpIdx + 1);
  mdpStack.push(target);
  mdpURLs.push(window.location.href);
  mdpIdx = mdpStack.length - 1;
  mdpSaveNav();
}
// Sync stack with the file we ended up on (covers static-mode
// back/forward navigations between separate file:// pages).
(function () {
  const cur = window.mdpCurrentFile;
  const curURL = window.location.href;
  if (!cur) return;
  if (mdpIdx >= 0 && mdpStack[mdpIdx] === cur) {
  } else if (mdpIdx >= 1 && mdpStack[mdpIdx - 1] === cur) {
    mdpIdx--;
  } else if (mdpIdx >= 0 && mdpStack[mdpIdx + 1] === cur) {
    mdpIdx++;
  } else {
    mdpStack = mdpStack.slice(0, mdpIdx + 1);
    mdpURLs = mdpURLs.slice(0, mdpIdx + 1);
    mdpStack.push(cur);
    mdpURLs.push(curURL);
    mdpIdx = mdpStack.length - 1;
  }
  // Refresh URL at current idx; back-restored pages may land at a
  // slightly-different URL (query string, hash, normalization).
  if (mdpIdx >= 0) mdpURLs[mdpIdx] = curURL;
  mdpSaveNav();
})();
// In static mode (file://) navigate via stored URLs so we don't
// depend on window.history.forward(), which is unreliable for
// cross-file:// nav in chrome --app= popups.
const mdpUseURLNav = window.location.protocol === 'file:';
function mdpGoBack() {
  if (mdpIdx <= 0) return;
  if (mdpUseURLNav && mdpURLs[mdpIdx - 1]) {
    window.location.href = mdpURLs[mdpIdx - 1];
  } else {
    window.history.back();
  }
}
function mdpGoForward() {
  if (mdpIdx >= mdpStack.length - 1) return;
  if (mdpUseURLNav && mdpURLs[mdpIdx + 1]) {
    window.location.href = mdpURLs[mdpIdx + 1];
  } else {
    window.history.forward();
  }
}
if (mdpBackBtn) mdpBackBtn.addEventListener('click', mdpGoBack);
if (mdpFwdBtn)  mdpFwdBtn.addEventListener('click', mdpGoForward);
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
// Hash-triggered install-chrome toast. Caller appends
// #mdp-install-chrome to the URL when falling back to a non-chromium
// browser; we surface the banner once (localStorage-gated) for 5s,
// then strip the hash so reloads/back-forward don't replay it.
(function () {
  if (window.location.hash !== '#mdp-install-chrome') return;
  history.replaceState(null, '', window.location.pathname + window.location.search);
  let seen = false;
  try { seen = localStorage.getItem('mdpInstallToastSeen') === '1'; } catch (_) {}
  if (seen) return;
  try { localStorage.setItem('mdpInstallToastSeen', '1'); } catch (_) {}
  const el = document.getElementById('mdp-install-toast');
  if (!el) return;
  el.hidden = false;
  void el.offsetWidth;
  el.classList.add('visible');
  setTimeout(() => {
    el.classList.remove('visible');
    setTimeout(() => { el.hidden = true; }, 250);
  }, 5000);
})();
// mdpStaticToast is the target of javascript:... hrefs that static
// mode emits for links it can't honour (out-of-tree, missing,
// unsupported, over-cap). The payload is URI-encoded; decode and
// hand off to mdpShowToast.
function mdpStaticToast(encoded) {
  mdpShowToast(decodeURIComponent(encoded));
}
window.mdpStaticToast = mdpStaticToast;
document.addEventListener('click', (e) => {
  if (e.defaultPrevented) return;
  const a = e.target.closest('a');
  if (!a) return;
  const href = a.getAttribute('href');
  if (!href) return;
  if (!/^(https?|mailto|tel|ftp|ftps):/i.test(href)) return;
  e.preventDefault();
  window.open(href, '_blank', 'noopener,noreferrer');
});
window.__mdpMark && window.__mdpMark('before hljs script');
__HLJS_SCRIPT__
window.__mdpMark && window.__mdpMark('after hljs script');
__HLJS_HIGHLIGHT_CALL__
window.__mdpMark && window.__mdpMark('after hljs highlight');
__KATEX_SCRIPT__
__KATEX_AUTORENDER_SCRIPT__
window.__mdpMark && window.__mdpMark('after katex scripts');
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
window.__mdpMark && window.__mdpMark('after katex render');
__MERMAID_SCRIPT__
__MERMAID_INIT__
window.__mdpMark && window.__mdpMark('after mermaid init');
const mdpCopyIconSVG = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>';
const mdpCheckIconSVG = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="20 6 9 17 4 12"></polyline></svg>';
function mdpAttachCopyButtons() {
  document.querySelectorAll('#content pre > code').forEach((code) => {
    const pre = code.parentElement;
    if (pre.querySelector('.mdp-copy-btn')) return;
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'mdp-copy-btn';
    btn.setAttribute('aria-label', 'Copy code');
    btn.title = 'Copy';
    btn.innerHTML = mdpCopyIconSVG;
    btn.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      const text = code.innerText;
      navigator.clipboard.writeText(text).then(() => {
        btn.innerHTML = mdpCheckIconSVG;
        btn.classList.add('mdp-copy-done');
        btn.title = 'Copied';
        clearTimeout(btn._mdpTimer);
        btn._mdpTimer = setTimeout(() => {
          btn.innerHTML = mdpCopyIconSVG;
          btn.classList.remove('mdp-copy-done');
          btn.title = 'Copy';
        }, 1500);
      }).catch(() => {
        btn.title = 'Copy failed';
      });
    });
    pre.appendChild(btn);
  });
}
window.mdpAttachCopyButtons = mdpAttachCopyButtons;
mdpAttachCopyButtons();
__VIM_KEYS__
__SHARED_NAV_SCRIPT__
__TREE_SCRIPT__
__TOC_SCRIPT__
__FINDER_SCRIPT__
__SEARCH_SCRIPT__
__VISUAL_SELECT_SCRIPT__
__WS_SCRIPT__
</script>
</body>
</html>`

// currentFile is the absolute path the click handler resolves relative
// hrefs against; empty when no file context applies (ad-hoc RenderBytes
// callers). wsPort > 0 embeds the WS client. fileTree gates the
// Tab-toggle sidebar; fuzzyFinder gates the Ctrl+P fuzzy picker. Either
// flag enables the shared data plumbing (mdpEnsureTreeData /
// mdpTreeNavigate) so the live UI has something to call. staticTreeJSON
// is the JSON literal embedded as window.mdpStaticTree in static mode
// (only meaningful when at least one of fileTree/fuzzyFinder is on);
// empty in WS mode (the page fetches /tree on demand).
// PageOptions configures BuildPage. All fields are optional except Body,
// Theme, and CurrentFile (CurrentFile may be empty for ad-hoc renders).
// AskCardWidth (px) and AskCardHeight (vh, 1-100) override in-code
// defaults; 0 keeps them. KeyOverrides remaps default action -> key
// strings; unknown actions are ignored.
type PageOptions struct {
	Body           string
	Theme          string
	WSPort         int
	ExtraCSS       string
	Colemak        bool
	CurrentFile    string
	FileTree       bool
	FuzzyFinder    bool
	StaticTreeJSON string
	Hop            bool
	Visual         bool
	Ask            bool
	KeyOverrides   map[string]string
	AskCardWidth   int
	AskCardHeight  int
}

func BuildPage(opts PageOptions) string {
	body := opts.Body
	theme := opts.Theme
	wsPort := opts.WSPort
	extraCSS := opts.ExtraCSS
	colemak := opts.Colemak
	currentFile := opts.CurrentFile
	fileTree := opts.FileTree
	fuzzyFinder := opts.FuzzyFinder
	staticTreeJSON := opts.StaticTreeJSON
	hop := opts.Hop
	visual := opts.Visual
	ask := opts.Ask
	keyOverrides := opts.KeyOverrides
	askCardWidth := opts.AskCardWidth
	askCardHeight := opts.AskCardHeight
	// Make every <img> async + lazy so external image fetches
	// (shields.io badges, remote screenshots, etc.) don't block
	// first-contentful-paint. Measured ~500ms cold-start improvement
	// on READMEs with a single shields.io badge.
	body = MarkImagesAsync(body)
	keys := resolveKeyBindings(colemak, keyOverrides)

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
	// fmt.Sprintf("%q", s) Go-quotes the path into a valid JS string
	// literal (handles backslashes, quotes, unicode escapes). Empty
	// currentFile becomes "" and the click handler no-ops.
	currentFileJS := fmt.Sprintf("%q", currentFile)
	title := html.EscapeString(titleFor(currentFile))

	katexCSSOut, katexJSOut, katexAutoRenderJSOut := "", "", ""
	if hasMath(body) {
		katexCSSOut = katexCSS
		katexJSOut = katexScript
		katexAutoRenderJSOut = katexAutoRenderScript
	}
	mermaidJSOut, mermaidInit := "", ""
	if hasMermaid(body) {
		mermaidJSOut = mermaidScript
		mermaidInit = `mermaid.initialize({startOnLoad:true,theme:'` + mermaidTheme(theme) + `'});`
	}
	hljsThemeOut, hljsScriptOut, hljsHighlightCall := "", "", ""
	if hasCodeFence(body) {
		hljsThemeOut = hljsThemeCSS
		hljsScriptOut = hljsScript
		hljsHighlightCall = "hljs.highlightAll();"
	}

	treeDOM, treeScript, finderDOM, finderScript, sharedNavScript := "", "", "", "", ""
	tocDOM := `<div id="mdp-toc" hidden>` +
		`<div class="mdp-toc-header"><span>Contents</span>` +
		`<button class="mdp-toc-close" aria-label="Close" title="Close">&times;</button>` +
		`</div><div class="mdp-toc-body"></div></div>`
	tocScript := buildTocScript(keys)
	searchDOM := `<div id="mdp-search" hidden>` +
		`<input id="mdp-search-input" type="text" autocomplete="off" spellcheck="false" placeholder="Find in page" aria-label="Find in page">` +
		`<span id="mdp-search-count" aria-live="polite"></span></div>`
	searchScript := buildSearchScript(keys)
	selectScript := buildSelectScript(keys, hop, visual, ask, askCardWidth, askCardHeight)
	if fileTree {
		treeDOM = `<div id="mdp-tree" hidden>` +
			`<div class="mdp-tree-header"><span>Files</span>` +
			`<button class="mdp-tree-close" aria-label="Close" title="Close">&times;</button>` +
			`</div><div class="mdp-tree-body"></div></div>`
		treeScript = buildTreeScript(keys)
	}
	if fuzzyFinder {
		finderDOM = `<div id="mdp-finder" hidden>` +
			`<input id="mdp-finder-input" type="text" autocomplete="off" spellcheck="false" placeholder="Find file" aria-label="Find file">` +
			`<ul id="mdp-finder-list" role="listbox"></ul></div>`
		finderScript = buildFinderScript(keys)
	}
	if fileTree || fuzzyFinder {
		treeData := ""
		if staticTreeJSON != "" {
			treeData = "window.mdpStaticTree = " + staticTreeJSON + ";"
		}
		sharedNavScript = strings.ReplaceAll(sharedNavScriptTemplate, "__STATIC_TREE_DATA__", treeData)
	}

	return strings.NewReplacer(
		"__HLJS_THEME_CSS__", hljsThemeOut,
		"__CSS_VARS__", cssVars,
		"__CSS_COMMON__", CSSCommon,
		"__PANDOC_CSS__", pandocCSS,
		"__CHROME_CSS__", chromeCSS,
		"__KATEX_CSS__", katexCSSOut,
		"__EXTRA_CSS__", extraCSS,
		"__BODY__", body,
		"__TITLE__", title,
		"__CURRENT_FILE_JS__", currentFileJS,
		"__HLJS_SCRIPT__", hljsScriptOut,
		"__HLJS_HIGHLIGHT_CALL__", hljsHighlightCall,
		"__KATEX_SCRIPT__", katexJSOut,
		"__KATEX_AUTORENDER_SCRIPT__", katexAutoRenderJSOut,
		"__MERMAID_SCRIPT__", mermaidJSOut,
		"__MERMAID_INIT__", mermaidInit,
		"__VIM_KEYS__", vimKeys(keys, wsPort == 0),
		"__TREE_DOM__", treeDOM,
		"__TOC_DOM__", tocDOM,
		"__FINDER_DOM__", finderDOM,
		"__SEARCH_DOM__", searchDOM,
		"__SHARED_NAV_SCRIPT__", sharedNavScript,
		"__TREE_SCRIPT__", treeScript,
		"__TOC_SCRIPT__", tocScript,
		"__FINDER_SCRIPT__", finderScript,
		"__SEARCH_SCRIPT__", searchScript,
		"__VISUAL_SELECT_SCRIPT__", selectScript,
		"__WS_SCRIPT__", wsScript,
	).Replace(pageTemplate)
}

// titleFor formats the preview window title as "parent/basename",
// falling back to "md-preview" when no file context applies (ad-hoc
// RenderBytes callers).
func titleFor(currentFile string) string {
	if currentFile == "" {
		return "md-preview"
	}
	base := filepath.Base(currentFile)
	parent := filepath.Base(filepath.Dir(currentFile))
	if parent == "" || parent == "." || parent == "/" {
		return base
	}
	return parent + "/" + base
}

func hasMermaid(body string) bool {
	return strings.Contains(body, `class="mermaid"`)
}

func mermaidTheme(theme string) string {
	if theme == "light" {
		return "default"
	}
	return "dark"
}

func hasCodeFence(body string) bool {
	return strings.Contains(body, `<code class="language-`)
}

// Single-dollar delimiters are intentionally NOT detected: KaTeX
// auto-render isn't configured for them (prose like `Costs $5 and $10`
// produced too many false positives).
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

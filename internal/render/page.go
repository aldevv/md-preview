package render

import (
	"fmt"
	"html"
	"path/filepath"
	"strings"
)

// __DOWN__/__UP__/__RIGHT__ are replaced with j/k/l (qwerty) or n/e/i
// (colemak); h, d/u, g/G, q are layout-stable. __FWD__ is the shifted
// right key (qwerty L, colemak I) that drives nav history forward;
// shifted h (H) is layout-stable for back. __RELOAD_CASE__ becomes the
// `r` reload binding in static mode and is stripped in WS-backed modes,
// which drive their own refresh.
const vimKeysScriptTemplate = `
(() => {
    const STEP = 60;
    function isEditable(el) {
        if (!el) return false;
        const tag = el.tagName;
        return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable;
    }
    // Hold-to-scroll for d/u: a single press still does the half-page
    // smooth jump, but as soon as the OS reports e.repeat we switch to a
    // rAF-driven constant-rate scroll. Smooth-scroll requests stack and
    // each new one cancels the previous animation mid-flight, which is
    // why held d/u used to feel like it slowed down.
    let mdpHoldDir = 0;
    let mdpHoldRAF = null;
    function mdpHoldTick() {
        if (!mdpHoldDir) return;
        window.scrollBy({ top: mdpHoldDir * 14, behavior: 'auto' });
        mdpHoldRAF = requestAnimationFrame(mdpHoldTick);
    }
    function mdpStartHold(dir) {
        if (mdpHoldDir === dir) return;
        mdpStopHold();
        mdpHoldDir = dir;
        mdpHoldRAF = requestAnimationFrame(mdpHoldTick);
    }
    function mdpStopHold() {
        mdpHoldDir = 0;
        if (mdpHoldRAF) { cancelAnimationFrame(mdpHoldRAF); mdpHoldRAF = null; }
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
            case 'd':
                if (e.repeat) mdpStartHold(1);
                else window.scrollBy({ top:  h / 2, behavior: 'smooth' });
                break;
            case 'u':
                if (e.repeat) mdpStartHold(-1);
                else window.scrollBy({ top: -h / 2, behavior: 'smooth' });
                break;
            case 'g': window.scrollTo({ top: 0, behavior: 'smooth' }); break;
            case 'G': window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'smooth' }); break;
            case 'H': if (typeof mdpGoBack === 'function') mdpGoBack(); else return; break;
            case '__FWD__': if (typeof mdpGoForward === 'function') mdpGoForward(); else return; break;
            case 'q': window.close(); break;
            __RELOAD_CASE__
            default: return;
        }
        e.preventDefault();
    });
    document.addEventListener('keyup', (e) => {
        if (e.key === 'd' || e.key === 'u') mdpStopHold();
    });
    window.addEventListener('blur', mdpStopHold);
})();
`

func vimKeys(colemak, staticReload bool) string {
	down, up, right := "j", "k", "l"
	if colemak {
		down, up, right = "n", "e", "i"
	}
	s := strings.ReplaceAll(vimKeysScriptTemplate, "__DOWN__", down)
	s = strings.ReplaceAll(s, "__UP__", up)
	s = strings.ReplaceAll(s, "__RIGHT__", right)
	s = strings.ReplaceAll(s, "__FWD__", strings.ToUpper(right))
	reloadCase := ""
	if staticReload {
		reloadCase = "case 'r': location.reload(); break;"
	}
	s = strings.ReplaceAll(s, "__RELOAD_CASE__", reloadCase)
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

function mdpTreeNavigate(rel) {
  const data = mdpTreeData;
  if (!data) return;
  const target = data.root + '/' + rel;
  if (data.rendered) {
    const tmp = data.rendered[rel];
    if (!tmp) { mdpShowToast('not pre-rendered: ' + rel); return; }
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
      if (typeof mdpToggleTree === 'function') mdpToggleTree(false);
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
const treeScriptTemplate = `
let mdpTreeIsOpen = false;
let mdpTreeExpanded = JSON.parse(sessionStorage.getItem('mdpTreeExpanded') || '[]');

function mdpBuildTree(data) {
  const currentRel = mdpCurrentRel(data);
  const root = { folders: {}, files: [] };
  for (const rel of (data.files || [])) {
    const parts = rel.split('/');
    let node = root;
    for (let i = 0; i < parts.length - 1; i++) {
      const seg = parts[i];
      if (!node.folders[seg]) node.folders[seg] = { folders: {}, files: [] };
      node = node.folders[seg];
    }
    node.files.push({ name: parts[parts.length - 1], rel });
  }
  if (currentRel) {
    const segs = currentRel.split('/');
    let p = '';
    for (let i = 0; i < segs.length - 1; i++) {
      p = p ? p + '/' + segs[i] : segs[i];
      if (!mdpTreeExpanded.includes(p)) mdpTreeExpanded.push(p);
    }
    sessionStorage.setItem('mdpTreeExpanded', JSON.stringify(mdpTreeExpanded));
  }
  const wrapper = document.createElement('div');
  wrapper.appendChild(mdpBuildNode(root, '', currentRel));
  return wrapper;
}

function mdpBuildNode(node, prefix, currentRel) {
  const ul = document.createElement('ul');
  const folderNames = Object.keys(node.folders).sort();
  for (const name of folderNames) {
    const folderPath = prefix ? prefix + '/' + name : name;
    const li = document.createElement('li');
    li.className = 'mdp-tree-folder';
    const details = document.createElement('details');
    if (mdpTreeExpanded.includes(folderPath)) details.open = true;
    details.addEventListener('toggle', () => {
      const set = new Set(mdpTreeExpanded);
      if (details.open) set.add(folderPath); else set.delete(folderPath);
      mdpTreeExpanded = [...set];
      sessionStorage.setItem('mdpTreeExpanded', JSON.stringify(mdpTreeExpanded));
    });
    const summary = document.createElement('summary');
    summary.textContent = name;
    details.appendChild(summary);
    details.appendChild(mdpBuildNode(node.folders[name], folderPath, currentRel));
    li.appendChild(details);
    ul.appendChild(li);
  }
  for (const f of node.files) {
    const li = document.createElement('li');
    li.className = 'mdp-tree-file';
    if (f.rel === currentRel) li.classList.add('current');
    const a = document.createElement('a');
    a.textContent = f.name;
    a.href = '#';
    a.addEventListener('click', (ev) => {
      ev.preventDefault();
      mdpTreeNavigate(f.rel);
    });
    li.appendChild(a);
    ul.appendChild(li);
  }
  return ul;
}

async function mdpToggleTree(force) {
  const panel = document.getElementById('mdp-tree');
  if (!panel) return;
  const want = (force === true || force === false) ? force : !mdpTreeIsOpen;
  if (want) {
    const data = await mdpEnsureTreeData();
    if (!data) return;
    const body = panel.querySelector('.mdp-tree-body');
    body.innerHTML = '';
    if (!data.files || data.files.length === 0) {
      const empty = document.createElement('div');
      empty.className = 'mdp-tree-empty';
      empty.textContent = 'No previewable files found.';
      body.appendChild(empty);
    } else {
      body.appendChild(mdpBuildTree(data));
    }
    panel.hidden = false;
    mdpTreeIsOpen = true;
  } else {
    panel.hidden = true;
    mdpTreeIsOpen = false;
  }
}
window.mdpToggleTree = mdpToggleTree;

(function () {
  const closeBtn = document.querySelector('#mdp-tree .mdp-tree-close');
  if (closeBtn) closeBtn.addEventListener('click', () => mdpToggleTree(false));
})();

document.addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  const tag = (e.target && e.target.tagName) || '';
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
  if (e.target && e.target.isContentEditable) return;
  if (e.key === 'Tab') {
    e.preventDefault();
    mdpToggleTree();
    return;
  }
  if (e.key === 'Escape' && mdpTreeIsOpen) {
    e.preventDefault();
    mdpToggleTree(false);
  }
});
`

// finderScriptTemplate powers the Ctrl+P fuzzy file finder. Depends on
// sharedNavScriptTemplate (mdpEnsureTreeData, mdpTreeNavigate).
const finderScriptTemplate = `
let mdpFinderIsOpen = false;
let mdpFinderIdx = 0;
let mdpFinderMatches = [];

function mdpFuzzyMatch(query, candidate) {
  if (!query) return { score: 0, hits: [] };
  const smartCase = /[A-Z]/.test(query);
  const q = smartCase ? query : query.toLowerCase();
  const c = smartCase ? candidate : candidate.toLowerCase();
  const hits = [];
  let score = 0, qi = 0, lastHit = -2;
  for (let i = 0; i < c.length && qi < q.length; i++) {
    if (c[i] !== q[qi]) continue;
    const prev = i === 0 ? '/' : c[i - 1];
    const isBoundary = prev === '/' || prev === '_' || prev === '-' || prev === '.';
    const isConsecutive = i === lastHit + 1;
    let gain = 1;
    if (isConsecutive) gain += 3;
    if (isBoundary) gain += 5;
    score += gain;
    hits.push(i);
    lastHit = i;
    qi++;
  }
  if (qi < q.length) return null;
  score -= (c.length - hits[hits.length - 1]) * 0.1;
  return { score, hits };
}

function mdpFinderRender(query) {
  const list = document.getElementById('mdp-finder-list');
  if (!list) return;
  const data = mdpTreeData;
  const files = (data && data.files) || [];
  let scored;
  if (!query) {
    scored = files.slice(0, 50).map((rel) => ({ rel, hits: [] }));
  } else {
    const tmp = [];
    for (const rel of files) {
      const m = mdpFuzzyMatch(query, rel);
      if (m) tmp.push({ rel, score: m.score, hits: m.hits });
    }
    tmp.sort((a, b) => b.score - a.score || a.rel.localeCompare(b.rel));
    scored = tmp.slice(0, 50);
  }
  mdpFinderMatches = scored;
  if (mdpFinderIdx >= scored.length) mdpFinderIdx = Math.max(0, scored.length - 1);
  list.innerHTML = '';
  scored.forEach((m, i) => {
    const li = document.createElement('li');
    li.setAttribute('role', 'option');
    if (i === mdpFinderIdx) li.setAttribute('aria-selected', 'true');
    if (m.hits && m.hits.length) {
      const set = new Set(m.hits);
      for (let j = 0; j < m.rel.length; j++) {
        if (set.has(j)) {
          const mark = document.createElement('mark');
          mark.textContent = m.rel[j];
          li.appendChild(mark);
        } else {
          li.appendChild(document.createTextNode(m.rel[j]));
        }
      }
    } else {
      li.textContent = m.rel;
    }
    li.addEventListener('mousedown', (ev) => {
      ev.preventDefault();
      mdpFinderIdx = i;
      mdpFinderSubmit();
    });
    list.appendChild(li);
  });
}

function mdpFinderSubmit() {
  const pick = mdpFinderMatches[mdpFinderIdx];
  if (!pick) return;
  mdpFinderClose();
  mdpTreeNavigate(pick.rel);
}

async function mdpFinderOpen() {
  const panel = document.getElementById('mdp-finder');
  const input = document.getElementById('mdp-finder-input');
  if (!panel || !input) return;
  const data = await mdpEnsureTreeData();
  if (!data) return;
  mdpFinderIdx = 0;
  input.value = '';
  panel.hidden = false;
  mdpFinderIsOpen = true;
  mdpFinderRender('');
  input.focus();
}

function mdpFinderClose() {
  const panel = document.getElementById('mdp-finder');
  if (!panel) return;
  panel.hidden = true;
  mdpFinderIsOpen = false;
}
window.mdpFinderOpen = mdpFinderOpen;

document.addEventListener('keydown', (e) => {
  if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey) return;
  if (e.key !== 'p' && e.key !== 'P') return;
  if (e.target && e.target.id === 'mdp-finder-input') return;
  // swallow browser print
  e.preventDefault();
  if (mdpFinderIsOpen) { mdpFinderClose(); return; }
  mdpFinderOpen();
});

(function () {
  const input = document.getElementById('mdp-finder-input');
  if (!input) return;
  input.addEventListener('input', () => {
    mdpFinderIdx = 0;
    mdpFinderRender(input.value);
  });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') { e.preventDefault(); mdpFinderClose(); return; }
    if (e.key === 'Enter') { e.preventDefault(); mdpFinderSubmit(); return; }
    const isDown = e.key === 'ArrowDown' || (e.ctrlKey && (e.key === 'n' || e.key === 'N'));
    const isUp = e.key === 'ArrowUp' || (e.ctrlKey && (e.key === 'p' || e.key === 'P'));
    if (isDown || isUp) {
      e.preventDefault();
      const n = mdpFinderMatches.length;
      if (!n) return;
      mdpFinderIdx = (mdpFinderIdx + (isDown ? 1 : -1) + n) % n;
      mdpFinderRender(input.value);
      const sel = document.querySelector('#mdp-finder-list li[aria-selected="true"]');
      if (sel && sel.scrollIntoView) sel.scrollIntoView({ block: 'nearest' });
    }
  });
})();
`

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
        }
        fetch('/').then(r => r.text()).then(html => {
            const doc = new DOMParser().parseFromString(html, 'text/html');
            document.querySelector('#content').innerHTML =
                doc.querySelector('#content').innerHTML;
            if (typeof hljs !== 'undefined') hljs.highlightAll();
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
__FINDER_DOM__
<div id="content" class="markdown-body">
__BODY__
</div>
<div id="mdp-toast" hidden></div>
<div id="mdp-install-toast" hidden>Install Chromium-based browser for independent windows</div>
<script>
window.mdpCurrentFile = __CURRENT_FILE_JS__;
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
__HLJS_SCRIPT__
__HLJS_HIGHLIGHT_CALL__
__KATEX_SCRIPT__
__KATEX_AUTORENDER_SCRIPT__
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
__MERMAID_SCRIPT__
__MERMAID_INIT__
__VIM_KEYS__
__SHARED_NAV_SCRIPT__
__TREE_SCRIPT__
__FINDER_SCRIPT__
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
func BuildPage(body, theme string, wsPort int, extraCSS string, colemak bool, currentFile string, fileTree, fuzzyFinder bool, staticTreeJSON string) string {
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
	if fileTree {
		treeDOM = `<div id="mdp-tree" hidden>` +
			`<div class="mdp-tree-header"><span>Files</span>` +
			`<button class="mdp-tree-close" aria-label="Close" title="Close">&times;</button>` +
			`</div><div class="mdp-tree-body"></div></div>`
		treeScript = treeScriptTemplate
	}
	if fuzzyFinder {
		finderDOM = `<div id="mdp-finder" hidden>` +
			`<input id="mdp-finder-input" type="text" autocomplete="off" spellcheck="false" placeholder="Find file" aria-label="Find file">` +
			`<ul id="mdp-finder-list" role="listbox"></ul></div>`
		finderScript = finderScriptTemplate
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
		"__VIM_KEYS__", vimKeys(colemak, wsPort == 0),
		"__TREE_DOM__", treeDOM,
		"__FINDER_DOM__", finderDOM,
		"__SHARED_NAV_SCRIPT__", sharedNavScript,
		"__TREE_SCRIPT__", treeScript,
		"__FINDER_SCRIPT__", finderScript,
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

package render

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
    function key(action) { return KEYS[action] || ''; }
    function isKey(e, action) { return key(action) && e.key === key(action); }
    function isEditable(el) {
        if (!el) return false;
        const tag = el.tagName;
        return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable;
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
    document.addEventListener('keydown', (e) => {
        if (e.ctrlKey || e.metaKey || e.altKey) return;
        if (isEditable(e.target)) return;
        if (window.mdpTreeIsOpen || window.mdpFinderIsOpen || window.mdpSelectIsActive) return;
        const h = window.innerHeight;
        if (isKey(e, 'down')) {
            window.scrollBy({ top:  STEP, behavior: 'auto' });
        } else if (isKey(e, 'up')) {
            window.scrollBy({ top: -STEP, behavior: 'auto' });
        } else if (isKey(e, 'left')) {
            window.scrollBy({ left: -STEP, behavior: 'auto' });
        } else if (isKey(e, 'right')) {
            window.scrollBy({ left:  STEP, behavior: 'auto' });
        } else if (isKey(e, 'half_down')) {
            if (e.repeat) mdpStartHold(1, mdpHoldHalfPx);
            else window.scrollBy({ top:  h / 2, behavior: 'smooth' });
        } else if (isKey(e, 'half_up')) {
            if (e.repeat) mdpStartHold(-1, mdpHoldHalfPx);
            else window.scrollBy({ top: -h / 2, behavior: 'smooth' });
        } else if (isKey(e, 'full_down')) {
            if (e.repeat) mdpStartHold(1, mdpHoldFullPx);
            else window.scrollBy({ top:  h, behavior: 'smooth' });
        } else if (isKey(e, 'full_up')) {
            if (e.repeat) mdpStartHold(-1, mdpHoldFullPx);
            else window.scrollBy({ top: -h, behavior: 'smooth' });
        } else if (isKey(e, 'top')) {
            window.scrollTo({ top: 0, behavior: 'smooth' });
        } else if (isKey(e, 'bottom')) {
            window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'smooth' });
        } else if (isKey(e, 'history_back')) {
            if (typeof mdpGoBack === 'function') mdpGoBack(); else return;
        } else if (isKey(e, 'history_forward')) {
            if (typeof mdpGoForward === 'function') mdpGoForward(); else return;
        } else if (isKey(e, 'zoom_in')) {
            mdpApplyZoom(mdpReadZoom() + ZOOM_STEP);
        } else if (isKey(e, 'zoom_out')) {
            mdpApplyZoom(mdpReadZoom() - ZOOM_STEP);
        } else if (isKey(e, 'zoom_reset')) {
            mdpApplyZoom(1);
        } else if (isKey(e, 'close')) {
            window.close();
        } else if (__RELOAD_CONDITION__) {
            location.reload();
        } else {
            return;
        }
        e.preventDefault();
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
	if colemak {
		down, up, right = "n", "e", "i"
		forward = "I"
	}
	return KeyBindings{
		"down":            down,
		"up":              up,
		"left":            "h",
		"right":           right,
		"half_down":       "d",
		"half_up":         "u",
		"full_down":       "f",
		"full_up":         "b",
		"top":             "g",
		"bottom":          "G",
		"history_back":    "H",
		"history_forward": forward,
		"close":           "q",
		"reload":          "r",
		"tree_toggle":     "Tab",
		"tree_down":       down,
		"tree_up":         up,
		"tree_left":       "h",
		"tree_right":      right,
		"tree_open":       "Enter",
		"finder_open":     "Ctrl+p",
		"select_pick":     "s",
		"select_visual":   "v",
		"select_left":     "h",
		"select_right":    right,
		"select_down":     down,
		"select_up":       up,
		"select_word_next": "w",
		"select_word_prev": "b",
		"select_line_start": "0",
		"select_line_end":  "$",
		"select_top":       "g",
		"select_bottom":    "G",
		"select_yank":      "y",
		"select_toggle_lines": "L",
		"select_ask":          "c",
		"zoom_in":         "+",
		"zoom_out":        "-",
		"zoom_reset":      "0",
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
		b.WriteString(fmt.Sprintf("%q:%q", action, keys[action]))
	}
	b.WriteByte('}')
	return b.String()
}

func vimKeys(keys KeyBindings, staticReload bool) string {
	s := strings.ReplaceAll(vimKeysScriptTemplate, "__KEYS_JSON__", keysJSON(keys))
	reloadCondition := "false"
	if staticReload {
		reloadCondition = "isKey(e, 'reload')"
	}
	s = strings.ReplaceAll(s, "__RELOAD_CONDITION__", reloadCondition)
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
window.mdpTreeIsOpen = false;
let mdpTreeExpanded = JSON.parse(sessionStorage.getItem('mdpTreeExpanded') || '[]');
let mdpTreeActive = { kind: '', path: '' };

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
    // Hand-rolled disclosure (div + click) instead of <details>/<summary>:
    // Tab key + dynamically-inserted <details> crashes chrome's renderer
    // (V8 SIGILL on chrome 148, reproduces in a single-handler 30-line
    // page). Plain divs avoid chrome's focus traversal touching them.
    const summary = document.createElement('div');
    summary.className = 'mdp-tree-summary';
    summary.tabIndex = -1;
    summary.dataset.mdpTreeKind = 'folder';
    summary.dataset.mdpTreePath = folderPath;
    summary.textContent = name;
    const childUL = mdpBuildNode(node.folders[name], folderPath, currentRel);
    const initialOpen = mdpTreeExpanded.includes(folderPath);
    childUL.hidden = !initialOpen;
    if (initialOpen) summary.classList.add('open');
    summary.addEventListener('click', () => {
      mdpTreeSetActive(summary);
      mdpTreeToggleFolder(summary);
    });
    li.appendChild(summary);
    li.appendChild(childUL);
    ul.appendChild(li);
  }
  for (const f of node.files) {
    const li = document.createElement('li');
    li.className = 'mdp-tree-file';
    if (f.rel === currentRel) li.classList.add('current');
    const a = document.createElement('a');
    a.textContent = f.name;
    a.href = '#';
    a.dataset.mdpTreeKind = 'file';
    a.dataset.mdpTreePath = f.rel;
    a.addEventListener('click', (ev) => {
      ev.preventDefault();
      mdpTreeSetActive(a);
      mdpTreeNavigate(f.rel);
    });
    li.appendChild(a);
    ul.appendChild(li);
  }
  return ul;
}

function mdpTreeVisibleItems() {
  const panel = document.getElementById('mdp-tree');
  if (!panel || panel.hidden) return [];
  return Array.from(panel.querySelectorAll('.mdp-tree-summary, .mdp-tree-file > a'))
    .filter((el) => el.offsetParent !== null);
}

function mdpTreeSetActive(el) {
  if (!el) return;
  document.querySelectorAll('#mdp-tree .active').forEach((n) => n.classList.remove('active'));
  el.classList.add('active');
  mdpTreeActive = { kind: el.dataset.mdpTreeKind || '', path: el.dataset.mdpTreePath || '' };
  if (el.scrollIntoView) el.scrollIntoView({ block: 'nearest' });
}

function mdpTreeRestoreActive() {
  const items = mdpTreeVisibleItems();
  if (!items.length) return;
  let pick = null;
  if (mdpTreeActive.path) {
    pick = items.find((el) => el.dataset.mdpTreeKind === mdpTreeActive.kind && el.dataset.mdpTreePath === mdpTreeActive.path);
  }
  if (!pick) pick = document.querySelector('#mdp-tree .mdp-tree-file.current > a');
  if (!pick || pick.offsetParent === null) pick = items[0];
  mdpTreeSetActive(pick);
}

function mdpTreeMove(delta) {
  const items = mdpTreeVisibleItems();
  if (!items.length) return;
  const current = document.querySelector('#mdp-tree .active');
  const idx = Math.max(0, items.indexOf(current));
  mdpTreeSetActive(items[(idx + delta + items.length) % items.length]);
}

function mdpTreeToggleFolder(summary, wantOpen) {
  const childUL = summary && summary.nextElementSibling;
  if (!childUL) return false;
  const nowOpen = wantOpen === undefined ? childUL.hidden : wantOpen;
  childUL.hidden = !nowOpen;
  summary.classList.toggle('open', nowOpen);
  const folderPath = summary.dataset.mdpTreePath;
  const set = new Set(mdpTreeExpanded);
  if (nowOpen) set.add(folderPath); else set.delete(folderPath);
  mdpTreeExpanded = [...set];
  sessionStorage.setItem('mdpTreeExpanded', JSON.stringify(mdpTreeExpanded));
  return true;
}

function mdpTreeOpenActive() {
  const current = document.querySelector('#mdp-tree .active');
  if (!current) return;
  if (current.dataset.mdpTreeKind === 'file') {
    mdpTreeNavigate(current.dataset.mdpTreePath);
    return;
  }
  mdpTreeToggleFolder(current);
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
    window.mdpTreeIsOpen = true;
    mdpTreeRestoreActive();
  } else {
    panel.hidden = true;
    mdpTreeIsOpen = false;
    window.mdpTreeIsOpen = false;
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
  if (window.mdpSelectIsActive) return;
  if (e.key === __TREE_TOGGLE__) {
    e.preventDefault();
    mdpToggleTree();
    return;
  }
  if (e.key === 'Escape' && mdpTreeIsOpen) {
    e.preventDefault();
    mdpToggleTree(false);
    return;
  }
  if (!mdpTreeIsOpen) return;
  const isDown = e.key === 'ArrowDown' || e.key === __TREE_DOWN__;
  const isUp = e.key === 'ArrowUp' || e.key === __TREE_UP__;
  const isLeft = e.key === 'ArrowLeft' || e.key === __TREE_LEFT__;
  const isRight = e.key === 'ArrowRight' || e.key === __TREE_RIGHT__;
  if (isDown || isUp) {
    e.preventDefault();
    mdpTreeMove(isDown ? 1 : -1);
    return;
  }
  if (e.key === __TREE_OPEN__) {
    e.preventDefault();
    mdpTreeOpenActive();
    return;
  }
  const current = document.querySelector('#mdp-tree .active');
  if (isRight && current && current.dataset.mdpTreeKind === 'folder') {
    e.preventDefault();
    if (mdpTreeToggleFolder(current, true)) mdpTreeRestoreActive();
    return;
  }
  if (isLeft && current && current.dataset.mdpTreeKind === 'folder') {
    e.preventDefault();
    if (mdpTreeToggleFolder(current, false)) mdpTreeRestoreActive();
  }
});
`

func jsString(s string) string {
	return fmt.Sprintf("%q", s)
}

func buildTreeScript(keys KeyBindings) string {
	s := strings.ReplaceAll(treeScriptTemplate, "__TREE_TOGGLE__", jsString(keys["tree_toggle"]))
	s = strings.ReplaceAll(s, "__TREE_DOWN__", jsString(keys["tree_down"]))
	s = strings.ReplaceAll(s, "__TREE_UP__", jsString(keys["tree_up"]))
	s = strings.ReplaceAll(s, "__TREE_LEFT__", jsString(keys["tree_left"]))
	s = strings.ReplaceAll(s, "__TREE_RIGHT__", jsString(keys["tree_right"]))
	s = strings.ReplaceAll(s, "__TREE_OPEN__", jsString(keys["tree_open"]))
	return s
}

// finderScriptTemplate powers the Ctrl+P fuzzy file finder. Depends on
// sharedNavScriptTemplate (mdpEnsureTreeData, mdpTreeNavigate).
const finderScriptTemplate = `
let mdpFinderIsOpen = false;
window.mdpFinderIsOpen = false;
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
  window.mdpFinderIsOpen = true;
  mdpFinderRender('');
  input.focus();
}

function mdpFinderClose() {
  const panel = document.getElementById('mdp-finder');
  if (!panel) return;
  panel.hidden = true;
  mdpFinderIsOpen = false;
  window.mdpFinderIsOpen = false;
}
window.mdpFinderOpen = mdpFinderOpen;

document.addEventListener('keydown', (e) => {
  if (!mdpMatchesKeySpec(e, __FINDER_OPEN__)) return;
  if (e.target && e.target.id === 'mdp-finder-input') return;
  if (window.mdpSelectIsActive) return;
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

func buildFinderScript(keys KeyBindings) string {
	return strings.ReplaceAll(finderScriptTemplate, "__FINDER_OPEN__", jsString(keys["finder_open"]))
}

const selectScriptTemplate = `
(() => {
  const HOP_ENABLED = __HOP_ENABLED__;
  const VISUAL_ENABLED = __VISUAL_ENABLED__;
  const ASK_ENABLED = __ASK_ENABLED__;
  const KEYS = __SELECT_KEYS_JSON__;
  const SINGLE_LABELS = 'abcdefghijklmnopqrstuvwxyz';
  const MAX_LABELED_HITS = SINGLE_LABELS.length * SINGLE_LABELS.length;
  const state = {
    mode: 'idle',
    anchor: null,
    head: null,
    labels: new Map(),
    overlays: [],
    lineRAF: null,
    lineListening: false,
    labelLen: 1,
    partialKey: '',
    countBuffer: '',
    askAbort: null,
    askEl: null,
    askInputEl: null,
    askPillEl: null,
    askStarEl: null,
    askLastPrompt: '',
    askAnchorRect: null,
    askSelectionText: ''
  };
  window.mdpSelectState = state;
  window.mdpSelectIsActive = false;
  function setMode(m) {
    state.mode = m;
    window.mdpSelectIsActive = m !== 'idle';
  }
  function generateLabels(n) {
    if (n <= SINGLE_LABELS.length) return SINGLE_LABELS.slice(0, n).split('');
    const out = [];
    for (const a of SINGLE_LABELS) {
      for (const b of SINGLE_LABELS) {
        out.push(a + b);
        if (out.length === n) return out;
      }
    }
    return out;
  }

  function key(action) { return KEYS[action] || ''; }
  function isKey(e, action) { return key(action) && e.key === key(action); }
  function isEditable(el) {
    if (!el) return false;
    const tag = el.tagName;
    return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable;
  }
  function blockedByOverlay() {
    return (typeof mdpFinderIsOpen !== 'undefined' && mdpFinderIsOpen) ||
      (typeof mdpTreeIsOpen !== 'undefined' && mdpTreeIsOpen);
  }
  function contentEl() { return document.getElementById('content'); }
  function insideContent(node) {
    const content = contentEl();
    if (!content || !node) return false;
    const el = node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement;
    return !!(el && content.contains(el));
  }
  function mdpSelectToast(msg) {
    if (typeof mdpShowToast === 'function') mdpShowToast(msg);
  }
  function clearOverlays(kind) {
    state.overlays = state.overlays.filter((el) => {
      if (!kind || el.dataset.mdpSelectKind === kind) {
        el.remove();
        return false;
      }
      return true;
    });
  }
  function mdpSelectStopLineNumbers() {
    if (!state.lineListening) return;
    window.removeEventListener('scroll', mdpSelectScheduleLineNumbers);
    window.removeEventListener('resize', mdpSelectScheduleLineNumbers);
    state.lineListening = false;
    if (state.lineRAF) {
      cancelAnimationFrame(state.lineRAF);
      state.lineRAF = null;
    }
  }
  function mdpSelectReset(keepSelection) {
    clearOverlays();
    mdpSelectStopLineNumbers();
    if (ASK_ENABLED) { mdpAskCloseAll(); mdpAskHideSelectionStar(); }
    document.body.classList.remove('mdp-select-mode');
    if (!keepSelection) {
      const sel = window.getSelection();
      if (sel) sel.removeAllRanges();
    }
    setMode('idle');
    state.anchor = null;
    state.head = null;
    state.labels.clear();
    state.labelLen = 1;
    state.partialKey = '';
    state.countBuffer = '';
  }
  function mdpSelectBail() {
    mdpSelectReset(false);
  }
  window.mdpSelectBail = mdpSelectBail;

  function mdpSelectVisibleTextNodes() {
    const root = contentEl();
    if (!root) return [];
    const nodes = [];
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
      acceptNode(node) {
        if (!node.nodeValue) return NodeFilter.FILTER_REJECT;
        const parent = node.parentElement;
        if (!parent) return NodeFilter.FILTER_REJECT;
        if (parent.closest('script,style')) return NodeFilter.FILTER_REJECT;
        const rect = parent.getBoundingClientRect();
        if (rect.bottom < -200 || rect.top > window.innerHeight + 200) return NodeFilter.FILTER_REJECT;
        return NodeFilter.FILTER_ACCEPT;
      }
    });
    for (let n = walker.nextNode(); n; n = walker.nextNode()) nodes.push(n);
    return nodes;
  }

  function textPoint(node, offset) {
    return { node, offset: Math.max(0, Math.min(offset, node.nodeValue.length)) };
  }
  function pointCompare(a, b) {
    if (a.node === b.node) return a.offset - b.offset;
    const pos = a.node.compareDocumentPosition(b.node);
    if (pos & Node.DOCUMENT_POSITION_FOLLOWING) return -1;
    if (pos & Node.DOCUMENT_POSITION_PRECEDING) return 1;
    return 0;
  }
  function mdpSelectMakeSelection(start, end) {
    const range = document.createRange();
    range.setStart(start.node, start.offset);
    range.setEnd(end.node, end.offset);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
  }
  function mdpSelectRebuildRange() {
    if (!state.anchor || !state.head) return;
    let start = state.anchor, end = state.head;
    if (pointCompare(end, start) < 0) {
      start = state.head;
      end = state.anchor;
    }
    mdpSelectMakeSelection(start, end);
    if (ASK_ENABLED) mdpAskShowSelectionStar();
  }

  function mdpSelectNodeAfter(node, dir) {
    const nodes = mdpSelectVisibleTextNodes();
    const idx = nodes.indexOf(node);
    if (idx < 0) return null;
    return nodes[idx + dir] || null;
  }
  function mdpSelectMoveChar(delta) {
    if (!state.head || state.mode !== 'visual') return;
    let node = state.head.node;
    let offset = state.head.offset + delta;
    while (node && offset < 0) {
      node = mdpSelectNodeAfter(node, -1);
      if (node) offset = node.nodeValue.length + offset;
    }
    while (node && offset > node.nodeValue.length) {
      offset -= node.nodeValue.length;
      node = mdpSelectNodeAfter(node, 1);
    }
    if (!node) return;
    state.head = textPoint(node, offset);
    mdpSelectRebuildRange();
  }

  // j/k move head to the start/end of the next/previous visible text
  // block. Not pixel-accurate visual-line motion, but close enough for
  // a markdown preview where each <p>/<li>/<h*> is a unit.
  function mdpSelectMoveLine(delta) {
    if (!state.head || state.mode !== 'visual') return;
    const nodes = mdpSelectVisibleTextNodes();
    const idx = nodes.indexOf(state.head.node);
    if (idx < 0) return;
    const targetIdx = Math.max(0, Math.min(nodes.length - 1, idx + delta));
    const next = nodes[targetIdx];
    if (!next || next === state.head.node) return;
    state.head = textPoint(next, delta > 0 ? next.nodeValue.length : 0);
    mdpSelectRebuildRange();
  }

  function mdpSelectMoveToLineStart() {
    if (!state.head || state.mode !== 'visual') return;
    state.head = textPoint(state.head.node, 0);
    mdpSelectRebuildRange();
  }
  function mdpSelectMoveToLineEnd() {
    if (!state.head || state.mode !== 'visual') return;
    state.head = textPoint(state.head.node, state.head.node.nodeValue.length);
    mdpSelectRebuildRange();
  }
  function mdpSelectMoveToTop() {
    if (state.mode !== 'visual') return;
    const nodes = mdpSelectVisibleTextNodes();
    if (!nodes.length) return;
    state.head = textPoint(nodes[0], 0);
    mdpSelectRebuildRange();
  }
  function mdpSelectMoveToBottom() {
    if (state.mode !== 'visual') return;
    const nodes = mdpSelectVisibleTextNodes();
    if (!nodes.length) return;
    const last = nodes[nodes.length - 1];
    state.head = textPoint(last, last.nodeValue.length);
    mdpSelectRebuildRange();
  }
  function mdpSelectMoveWord(delta) {
    if (!state.head || state.mode !== 'visual') return;
    const ws = /\s/;
    let node = state.head.node;
    let offset = state.head.offset;
    if (delta > 0) {
      while (node) {
        const text = node.nodeValue;
        while (offset < text.length && !ws.test(text[offset])) offset++;
        while (offset < text.length && ws.test(text[offset])) offset++;
        if (offset < text.length) {
          state.head = textPoint(node, offset);
          mdpSelectRebuildRange();
          return;
        }
        node = mdpSelectNodeAfter(node, 1);
        offset = 0;
        if (node) {
          const t = node.nodeValue;
          while (offset < t.length && ws.test(t[offset])) offset++;
          if (offset < t.length) {
            state.head = textPoint(node, offset);
            mdpSelectRebuildRange();
            return;
          }
        }
      }
      return;
    }
    while (node) {
      const text = node.nodeValue;
      let i = offset - 1;
      while (i >= 0 && ws.test(text[i])) i--;
      while (i >= 0 && !ws.test(text[i])) i--;
      const wordStart = i + 1;
      if (wordStart < offset) {
        state.head = textPoint(node, wordStart);
        mdpSelectRebuildRange();
        return;
      }
      node = mdpSelectNodeAfter(node, -1);
      if (node) offset = node.nodeValue.length;
    }
  }

  function mdpSelectPaintLineNumbers() {
    clearOverlays('line');
    const blocks = document.querySelectorAll('#content [data-line]');
    for (const el of blocks) {
      const rect = el.getBoundingClientRect();
      if (rect.bottom < 0 || rect.top > window.innerHeight) continue;
      const span = document.createElement('span');
      span.className = 'mdp-select-linenum';
      span.dataset.mdpSelectKind = 'line';
      span.style.top = Math.round(rect.top + window.scrollY) + 'px';
      span.textContent = el.dataset.line;
      document.body.appendChild(span);
      state.overlays.push(span);
    }
  }
  function mdpSelectScheduleLineNumbers() {
    if (state.lineRAF) return;
    state.lineRAF = requestAnimationFrame(() => {
      state.lineRAF = null;
      if (state.mode === 'visual') mdpSelectPaintLineNumbers();
    });
  }
  function mdpSelectStartLineNumbers() {
    mdpSelectPaintLineNumbers();
    if (state.lineListening) return;
    window.addEventListener('scroll', mdpSelectScheduleLineNumbers);
    window.addEventListener('resize', mdpSelectScheduleLineNumbers);
    state.lineListening = true;
  }

  function mdpSelectJumpTo(node, offset) {
    clearOverlays('label');
    const start = textPoint(node, offset);
    if (VISUAL_ENABLED) {
      setMode('visual');
      state.anchor = start;
      state.head = textPoint(node, offset + 1);
      document.body.classList.add('mdp-select-mode');
      mdpSelectRebuildRange();
      return;
    }
    mdpSelectMakeSelection(start, textPoint(node, offset + 1));
    mdpSelectReset(true);
  }
  window.mdpSelectJumpTo = mdpSelectJumpTo;

  function mdpSelectPaintLabels(char) {
    clearOverlays('label');
    state.labels.clear();
    state.partialKey = '';
    const hits = [];
    for (const node of mdpSelectVisibleTextNodes()) {
      const text = node.nodeValue;
      for (let i = 0; i < text.length; i++) {
        if (text[i] === char) hits.push({ node, offset: i });
        if (hits.length > MAX_LABELED_HITS) {
          mdpSelectToast('too many, scroll closer');
          mdpSelectBail();
          return;
        }
      }
    }
    if (!hits.length) {
      mdpSelectToast('no matches');
      mdpSelectBail();
      return;
    }
    const labelSeq = generateLabels(hits.length);
    state.labelLen = hits.length > SINGLE_LABELS.length ? 2 : 1;
    setMode('labeled');
    hits.forEach((hit, i) => {
      const label = labelSeq[i];
      const range = document.createRange();
      range.setStart(hit.node, hit.offset);
      range.setEnd(hit.node, hit.offset + 1);
      const rect = range.getBoundingClientRect();
      if (!rect || rect.width === 0 && rect.height === 0) return;
      const span = document.createElement('span');
      span.className = 'mdp-select-label';
      span.dataset.mdpSelectKind = 'label';
      span.dataset.mdpSelectLabel = label;
      span.style.left = Math.round(rect.left + window.scrollX) + 'px';
      span.style.top = Math.round(rect.top + window.scrollY) + 'px';
      span.textContent = label;
      document.body.appendChild(span);
      state.overlays.push(span);
      state.labels.set(label, hit);
    });
  }

  function mdpSelectFilterLabels(prefix) {
    for (const span of state.overlays) {
      if (span.dataset.mdpSelectKind !== 'label') continue;
      const label = span.dataset.mdpSelectLabel || '';
      if (label.startsWith(prefix)) {
        span.style.display = '';
        span.textContent = label.slice(prefix.length) || label;
      } else {
        span.style.display = 'none';
      }
    }
  }

  function mdpSelectStartPick() {
    mdpSelectReset(false);
    setMode('pickChar');
    document.body.classList.add('mdp-select-mode');
  }
  window.mdpSelectStartPick = mdpSelectStartPick;

  function rangeFromPoint(x, y) {
    if (document.caretRangeFromPoint) return document.caretRangeFromPoint(x, y);
    if (document.caretPositionFromPoint) {
      const pos = document.caretPositionFromPoint(x, y);
      if (!pos) return null;
      const range = document.createRange();
      range.setStart(pos.offsetNode, pos.offset);
      range.collapse(true);
      return range;
    }
    return null;
  }
  function firstTextNode(el) {
    if (!el) return null;
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT, {
      acceptNode(node) {
        return node.nodeValue ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT;
      }
    });
    return walker.nextNode();
  }
  function mdpSelectStartCenter() {
    mdpSelectReset(false);
    const x = Math.floor(window.innerWidth / 2);
    const y = Math.floor(window.innerHeight / 2);
    let range = rangeFromPoint(x, y);
    if (range && range.startContainer.nodeType === Node.TEXT_NODE && insideContent(range.startContainer)) {
      mdpSelectJumpTo(range.startContainer, range.startOffset);
      return;
    }
    for (const el of document.elementsFromPoint(x, y)) {
      if (!insideContent(el)) continue;
      const node = firstTextNode(el);
      if (node) {
        mdpSelectJumpTo(node, 0);
        return;
      }
    }
    mdpSelectToast('no text at viewport center');
  }
  window.mdpSelectStartCenter = mdpSelectStartCenter;

  async function mdpSelectYank() {
    const text = window.getSelection().toString();
    try {
      await navigator.clipboard.writeText(text);
      mdpSelectToast('yanked ' + text.length + ' chars');
      mdpSelectBail();
    } catch (err) {
      mdpSelectToast('clipboard failed: ' + err.message);
    }
  }

  function mdpAskSnapshotSelection() {
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0) return null;
    const text = sel.toString();
    if (!text) return null;
    const rect = sel.getRangeAt(0).getBoundingClientRect();
    return { text, rect };
  }
  function mdpAskCloseAll() {
    if (state.askAbort) { try { state.askAbort.abort(); } catch (_) {} state.askAbort = null; }
    for (const k of ['askInputEl', 'askPillEl', 'askEl']) {
      if (state[k]) { state[k].remove(); state[k] = null; }
    }
  }
  // ASK_STAR_SVG is the Gemini-style four-point sparkle used for both
  // the floating selection icon and the top-right whole-file icon.
  const ASK_STAR_SVG = '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">' +
    '<path d="M12 2 C12 8 14 10 22 12 C14 14 12 16 12 22 C12 16 10 14 2 12 C10 10 12 8 12 2 Z" fill="currentColor"/>' +
    '</svg>';
  function mdpAskMakeStarButton(extraClass, label) {
    const btn = document.createElement('button');
    btn.className = 'mdp-ask-star' + (extraClass ? ' ' + extraClass : '');
    btn.setAttribute('aria-label', label || 'Ask Claude');
    btn.title = label || 'Ask Claude';
    btn.innerHTML = ASK_STAR_SVG;
    btn.addEventListener('mousedown', (e) => e.preventDefault());
    return btn;
  }
  function mdpAskShowSelectionStar() {
    if (!ASK_ENABLED || state.mode !== 'visual') return;
    if (!mdpAskServerReachable()) return;
    if (state.askStarEl) { state.askStarEl.remove(); state.askStarEl = null; }
    if (state.askInputEl || state.askPillEl || state.askEl) return;
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return;
    const rect = sel.getRangeAt(0).getBoundingClientRect();
    if (!(rect.width || rect.height)) return;
    const btn = mdpAskMakeStarButton('mdp-ask-star-floating', 'Ask Claude about selection');
    btn.addEventListener('click', () => mdpAskOpenInput());
    document.body.appendChild(btn);
    const left = Math.min(window.innerWidth - 28, rect.right + 4);
    const top = Math.max(8, rect.top - 4);
    btn.style.left = Math.round(left + window.scrollX) + 'px';
    btn.style.top = Math.round(top + window.scrollY) + 'px';
    state.askStarEl = btn;
  }
  function mdpAskHideSelectionStar() {
    if (state.askStarEl) { state.askStarEl.remove(); state.askStarEl = null; }
  }
  function mdpAskServerReachable() {
    // /ask is HTTP-only; static mode (file://) has no server to hit.
    return window.location.protocol !== 'file:';
  }
  function mdpAskSidecarURL() {
    const u = window.__MDP_SIDECAR_URL__;
    return (typeof u === 'string' && u) ? u : '';
  }
  function mdpAskPromoteViaSidecar() {
    const u = mdpAskSidecarURL();
    if (!u) { mdpSelectToast('promote unavailable'); return; }
    window.location.href = u + '/promote';
  }
  function mdpAskInstallFab() {
    if (!ASK_ENABLED) return;
    const reachable = mdpAskServerReachable();
    if (!reachable && !mdpAskSidecarURL()) return;
    if (document.getElementById('mdp-ask-fab')) return;
    const label = reachable ? 'Ask Claude about this file' : 'Enable Claude (switch to watch mode)';
    const fab = mdpAskMakeStarButton('mdp-ask-fab', label);
    fab.id = 'mdp-ask-fab';
    fab.addEventListener('click', () => {
      if (mdpAskServerReachable()) {
        mdpAskOpenWholeFile();
      } else {
        mdpAskPromoteViaSidecar();
      }
    });
    document.body.appendChild(fab);
  }
  function mdpAskOpenWholeFile() {
    if (!ASK_ENABLED) return;
    const content = document.getElementById('content');
    if (!content) return;
    const text = (content.textContent || '').trim();
    if (!text) { mdpSelectToast('nothing to ask about'); return; }
    state.askSelectionText = text;
    state.askAnchorRect = null;
    mdpAskShowInputUI();
  }
  function mdpAskOpenInput() {
    if (!ASK_ENABLED || state.mode !== 'visual') return;
    if (!mdpAskServerReachable()) { mdpSelectToast('ask requires mdp watch or mdp serve'); return; }
    const snap = mdpAskSnapshotSelection();
    if (!snap) { mdpSelectToast('no selection to ask about'); return; }
    state.askSelectionText = snap.text;
    state.askAnchorRect = snap.rect;
    mdpAskShowInputUI();
  }
  function mdpAskShowInputUI() {
    if (state.askEl) { state.askEl.remove(); state.askEl = null; }
    if (state.askPillEl) { state.askPillEl.remove(); state.askPillEl = null; }
    if (state.askInputEl) { state.askInputEl.remove(); state.askInputEl = null; }
    mdpAskHideSelectionStar();
    const wrap = document.createElement('div');
    wrap.className = 'mdp-ask-input';
    const input = document.createElement('input');
    input.type = 'text';
    input.spellcheck = false;
    input.autocomplete = 'off';
    input.placeholder = 'ask claude about the selection...';
    input.value = state.askLastPrompt || '';
    wrap.appendChild(input);
    document.body.appendChild(wrap);
    mdpAskPositionAnchored(wrap, state.askAnchorRect, { width: 360, height: 32, prefer: 'above' });
    state.askInputEl = wrap;
    input.focus();
    input.select();
    input.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter') {
        ev.preventDefault();
        const v = input.value.trim();
        if (v) mdpAskSubmit(v);
      } else if (ev.key === 'Escape') {
        ev.preventDefault();
        mdpAskCloseAll();
      }
    });
  }
  function mdpAskShowPill() {
    if (state.askInputEl) { state.askInputEl.remove(); state.askInputEl = null; }
    const pill = document.createElement('div');
    pill.className = 'mdp-ask-pill';
    pill.textContent = 'thinking';
    document.body.appendChild(pill);
    mdpAskPositionAnchored(pill, state.askAnchorRect, { width: 120, height: 28, prefer: 'above' });
    state.askPillEl = pill;
  }
  async function mdpAskSubmit(prompt) {
    state.askLastPrompt = prompt;
    mdpAskShowPill();
    const abort = new AbortController();
    state.askAbort = abort;
    try {
      const r = await fetch('/ask', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({ selection: state.askSelectionText, prompt }),
        signal: abort.signal
      });
      if (state.askAbort !== abort) return;
      state.askAbort = null;
      if (state.askPillEl) { state.askPillEl.remove(); state.askPillEl = null; }
      const data = await r.json().catch(() => ({}));
      if (!r.ok) {
        mdpSelectToast(data && data.error ? data.error : 'ask failed (' + r.status + ')');
        return;
      }
      mdpAskRenderCard(data.html || '');
    } catch (err) {
      if (err && err.name === 'AbortError') return;
      if (state.askPillEl) { state.askPillEl.remove(); state.askPillEl = null; }
      mdpSelectToast('ask failed: ' + err);
    } finally {
      if (state.askAbort === abort) state.askAbort = null;
    }
  }
  function mdpAskRenderCard(html) {
    if (state.askEl) { state.askEl.remove(); state.askEl = null; }
    const card = document.createElement('div');
    card.className = 'mdp-ask-card';
    const close = document.createElement('button');
    close.className = 'mdp-ask-card-close';
    close.setAttribute('aria-label', 'Close');
    close.textContent = String.fromCharCode(215);
    close.addEventListener('click', () => mdpAskCloseAll());
    const body = document.createElement('div');
    body.className = 'mdp-ask-card-body markdown-body';
    body.innerHTML = html;
    card.appendChild(close);
    card.appendChild(body);
    document.body.appendChild(card);
    mdpAskPositionCard(card, state.askAnchorRect);
    state.askEl = card;
  }
  function mdpAskPositionAnchored(el, rect, opts) {
    const w = opts.width, h = opts.height;
    if (!rect) {
      el.style.left = Math.max(8, (window.innerWidth - w) / 2) + 'px';
      el.style.top = Math.max(8, (window.innerHeight - h) / 2) + 'px';
      return;
    }
    let top = rect.top + window.scrollY - h - 6;
    if (top < window.scrollY + 8) top = rect.bottom + window.scrollY + 6;
    let left = rect.left + window.scrollX;
    if (left + w > window.scrollX + window.innerWidth - 8) {
      left = window.scrollX + window.innerWidth - w - 8;
    }
    if (left < window.scrollX + 8) left = window.scrollX + 8;
    el.style.left = Math.round(left) + 'px';
    el.style.top = Math.round(top) + 'px';
  }
  function mdpAskPositionCard(el, rect) {
    const margin = 8;
    const cardRect = el.getBoundingClientRect();
    const w = cardRect.width, h = cardRect.height;
    if (!rect) {
      el.style.left = Math.max(margin, (window.innerWidth - w) / 2) + 'px';
      el.style.top = Math.max(margin, (window.innerHeight - h) / 2) + 'px';
      return;
    }
    // Try right of the selection, then left, then below.
    const rightLeft = rect.right + margin;
    const leftLeft = rect.left - w - margin;
    let left;
    if (rightLeft + w <= window.innerWidth - margin) {
      left = rightLeft;
    } else if (leftLeft >= margin) {
      left = leftLeft;
    } else {
      left = Math.max(margin, (window.innerWidth - w) / 2);
    }
    let top = rect.top;
    if (top + h > window.innerHeight - margin) top = Math.max(margin, window.innerHeight - h - margin);
    el.style.left = Math.round(left + window.scrollX) + 'px';
    el.style.top = Math.round(top + window.scrollY) + 'px';
  }

  function mdpSelectOnKeydown(e) {
    if (isEditable(e.target) || blockedByOverlay()) return;
    if (state.mode === 'idle' || state.mode === 'visual') {
      if (HOP_ENABLED && isKey(e, 'select_pick')) {
        e.preventDefault();
        mdpSelectStartPick();
        return;
      }
      if (VISUAL_ENABLED && isKey(e, 'select_visual')) {
        e.preventDefault();
        mdpSelectStartCenter();
        return;
      }
      if (state.mode === 'idle') {
        // Idle-mode 'c' (select_ask) mirrors the AI icon: in static
        // mode it promotes via the sidecar; on the watch server it
        // opens the whole-file ask input directly.
        if (ASK_ENABLED && isKey(e, 'select_ask')) {
          if (mdpAskServerReachable()) {
            e.preventDefault();
            mdpAskOpenWholeFile();
          } else if (mdpAskSidecarURL()) {
            e.preventDefault();
            mdpAskPromoteViaSidecar();
          }
        }
        return;
      }
    }
    if (e.key === 'Escape') {
      e.preventDefault();
      if (state.askEl || state.askPillEl || state.askInputEl || state.askAbort) {
        mdpAskCloseAll();
        return;
      }
      mdpSelectBail();
      return;
    }
    if (state.mode === 'pickChar') {
      if (e.ctrlKey || e.metaKey || e.altKey || e.key.length !== 1) return;
      e.preventDefault();
      mdpSelectPaintLabels(e.key);
      return;
    }
    if (state.mode === 'labeled') {
      if (e.ctrlKey || e.metaKey || e.altKey || e.key.length !== 1) return;
      e.preventDefault();
      state.partialKey += e.key;
      if (state.partialKey.length < state.labelLen) {
        mdpSelectFilterLabels(state.partialKey);
        return;
      }
      const hit = state.labels.get(state.partialKey);
      if (!hit) {
        mdpSelectBail();
        return;
      }
      mdpSelectJumpTo(hit.node, hit.offset);
      return;
    }
    if (state.mode === 'visual') {
      // Digit prefix builds a count: '10h' moves 10 chars left. A bare
      // '0' (no buffer) is treated as a motion (line start) below.
      if (e.key.length === 1 && e.key >= '0' && e.key <= '9' &&
          !(e.key === '0' && !state.countBuffer)) {
        e.preventDefault();
        state.countBuffer += e.key;
        return;
      }
      const count = Math.max(1, parseInt(state.countBuffer || '1', 10));
      state.countBuffer = '';
      if (isKey(e, 'select_left')) {
        e.preventDefault();
        for (let i = 0; i < count; i++) mdpSelectMoveChar(-1);
      } else if (isKey(e, 'select_right')) {
        e.preventDefault();
        for (let i = 0; i < count; i++) mdpSelectMoveChar(1);
      } else if (isKey(e, 'select_down')) {
        e.preventDefault();
        for (let i = 0; i < count; i++) mdpSelectMoveLine(1);
      } else if (isKey(e, 'select_up')) {
        e.preventDefault();
        for (let i = 0; i < count; i++) mdpSelectMoveLine(-1);
      } else if (isKey(e, 'select_word_next')) {
        e.preventDefault();
        for (let i = 0; i < count; i++) mdpSelectMoveWord(1);
      } else if (isKey(e, 'select_word_prev')) {
        e.preventDefault();
        for (let i = 0; i < count; i++) mdpSelectMoveWord(-1);
      } else if (isKey(e, 'select_line_start')) {
        e.preventDefault();
        mdpSelectMoveToLineStart();
      } else if (isKey(e, 'select_line_end')) {
        e.preventDefault();
        mdpSelectMoveToLineEnd();
      } else if (isKey(e, 'select_top')) {
        e.preventDefault();
        mdpSelectMoveToTop();
      } else if (isKey(e, 'select_bottom')) {
        e.preventDefault();
        mdpSelectMoveToBottom();
      } else if (isKey(e, 'select_yank')) {
        e.preventDefault();
        mdpSelectYank();
      } else if (isKey(e, 'select_toggle_lines')) {
        e.preventDefault();
        if (state.lineListening) {
          mdpSelectStopLineNumbers();
          clearOverlays('line');
        } else {
          mdpSelectStartLineNumbers();
        }
      } else if (ASK_ENABLED && isKey(e, 'select_ask')) {
        e.preventDefault();
        mdpAskOpenInput();
      }
    }
  }
  document.addEventListener('keydown', mdpSelectOnKeydown, true);
  if (ASK_ENABLED) {
    mdpAskInstallFab();
    // The sidecar's /promote redirect appends ?open=ask so the user's
    // single click on the static-mode AI icon both promotes AND opens
    // the input on arrival. Query param (not a URL fragment) because
    // some Chromium/WebKit builds strip the fragment on cross-origin
    // 302 redirects.
    try {
      const params = new URLSearchParams(window.location.search);
      if (mdpAskServerReachable() && params.get('open') === 'ask') {
        params.delete('open');
        const q = params.toString();
        const url = window.location.pathname + (q ? '?' + q : '') + window.location.hash;
        try { history.replaceState(null, '', url); } catch (_) {}
        // Defer until after load + a paint frame so the browser's
        // default body-focus on page load doesn't immediately steal
        // focus back from the input we just opened.
        const openAndFocus = () => {
          mdpAskOpenWholeFile();
          requestAnimationFrame(() => {
            const i = document.querySelector('.mdp-ask-input input');
            if (i && document.activeElement !== i) i.focus();
          });
        };
        if (document.readyState === 'complete') setTimeout(openAndFocus, 0);
        else window.addEventListener('load', () => setTimeout(openAndFocus, 0), { once: true });
      }
    } catch (_) {}
  }
})();
`

func buildSelectScript(keys KeyBindings, hop, visual, ask bool) string {
	if !hop && !visual {
		return ""
	}
	s := strings.ReplaceAll(selectScriptTemplate, "__HOP_ENABLED__", fmt.Sprintf("%t", hop))
	s = strings.ReplaceAll(s, "__VISUAL_ENABLED__", fmt.Sprintf("%t", visual))
	s = strings.ReplaceAll(s, "__ASK_ENABLED__", fmt.Sprintf("%t", ask && visual))
	s = strings.ReplaceAll(s, "__SELECT_KEYS_JSON__", keysJSON(keys))
	return s
}

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
__FINDER_DOM__
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
__VIM_KEYS__
__SHARED_NAV_SCRIPT__
__TREE_SCRIPT__
__FINDER_SCRIPT__
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
func BuildPage(body, theme string, wsPort int, extraCSS string, colemak bool, currentFile string, fileTree, fuzzyFinder bool, staticTreeJSON string) string {
	return BuildPageWithKeys(body, theme, wsPort, extraCSS, colemak, currentFile, fileTree, fuzzyFinder, staticTreeJSON, false, false, false, nil)
}

// BuildPageWithKeys is BuildPage plus user key overrides. Unknown actions are
// ignored; an empty key disables that action.
func BuildPageWithKeys(body, theme string, wsPort int, extraCSS string, colemak bool, currentFile string, fileTree, fuzzyFinder bool, staticTreeJSON string, hop, visual, ask bool, keyOverrides map[string]string) string {
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
	selectScript := buildSelectScript(keys, hop, visual, ask)
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
		"__FINDER_DOM__", finderDOM,
		"__SHARED_NAV_SCRIPT__", sharedNavScript,
		"__TREE_SCRIPT__", treeScript,
		"__FINDER_SCRIPT__", finderScript,
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

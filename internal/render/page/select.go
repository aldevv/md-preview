package page

import (
	"fmt"
	"strings"
)

const selectScriptTemplate = `
(() => {
  const HOP_ENABLED = __HOP_ENABLED__;
  const VISUAL_ENABLED = __VISUAL_ENABLED__;
  const ASK_ENABLED = __ASK_ENABLED__;
  const ASK_CARD_WIDTH = __ASK_CARD_WIDTH__;
  const ASK_CARD_HEIGHT = __ASK_CARD_HEIGHT__;
  const KEYS = __SELECT_KEYS_JSON__;
  const SINGLE_LABELS = 'abcdefghijklmnopqrstuvwxyz';
  // 26^3 = 17576 — way more than ever fits visibly on screen, but
  // a finite cap keeps a pathological match (e.g. searching ' ' on a
  // huge doc) from painting tens of thousands of overlay divs.
  const MAX_LABELED_HITS = Math.pow(SINGLE_LABELS.length, 3);
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
    askLastResponse: '',
    askAnchorRect: null,
    askSelectionText: '',
    askHistoryIdx: -1
  };
  window.mdpSelectState = state;
  window.mdpSelectIsActive = false;
  function setMode(m) {
    state.mode = m;
    window.mdpSelectIsActive = m !== 'idle';
  }
  // labelLengthFor returns the shortest label width that can address n
  // hits using SINGLE_LABELS as the alphabet. n=1..26 → 1, 27..676 →
  // 2, 677..17576 → 3, etc.
  function labelLengthFor(n) {
    if (n <= 0) return 1;
    let k = 1;
    let cap = SINGLE_LABELS.length;
    while (cap < n) { k++; cap *= SINGLE_LABELS.length; }
    return k;
  }
  function generateLabels(n) {
    const k = labelLengthFor(n);
    if (k === 1) return SINGLE_LABELS.slice(0, n).split('');
    const out = [];
    const idx = new Array(k).fill(0);
    while (out.length < n) {
      let s = '';
      for (let i = 0; i < k; i++) s += SINGLE_LABELS[idx[i]];
      out.push(s);
      for (let i = k - 1; i >= 0; i--) {
        idx[i]++;
        if (idx[i] < SINGLE_LABELS.length) break;
        idx[i] = 0;
        if (i === 0) return out;
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
    const needle = char.toLowerCase();
    const hits = [];
    for (const node of mdpSelectVisibleTextNodes()) {
      const text = node.nodeValue;
      for (let i = 0; i < text.length; i++) {
        if (text[i].toLowerCase() === needle) hits.push({ node, offset: i });
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
    state.labelLen = labelLengthFor(hits.length);
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
    clearOverlays('frozen');
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
    // Show in static mode too when the sidecar is available — clicking
    // it routes through mdpAskOpenInput → mdpAskPromoteViaSidecar with
    // the current selection.
    if (!mdpAskServerReachable() && !mdpAskSidecarURL()) return;
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
  // selection (optional) is the text the user had selected on the
  // static page; the sidecar forwards it to the watch page via the
  // 302 query string so the input can open pre-loaded with that
  // selection. Selections longer than maxSelLen are truncated to keep
  // the URL under common browser limits (~8 KB).
  function mdpAskPromoteViaSidecar(selection) {
    const u = mdpAskSidecarURL();
    if (!u) { mdpSelectToast('promote unavailable'); return; }
    const params = new URLSearchParams();
    if (selection) {
      const maxSelLen = 4000;
      let s = selection;
      if (s.length > maxSelLen) s = s.slice(0, maxSelLen);
      params.set('sel', s);
    }
    // Pass scroll position as a fraction (0–1) so the watch page can
    // restore roughly where the user was — pages render with the same
    // markdown so the fraction maps closely enough.
    const max = Math.max(1, document.documentElement.scrollHeight - window.innerHeight);
    const frac = Math.min(1, Math.max(0, window.scrollY / max));
    if (frac > 0) params.set('scroll', frac.toFixed(4));
    const target = u + '/promote' + (params.toString() ? '?' + params.toString() : '');
    window.location.href = target;
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
  function mdpPickVisibleSearchHit() {
    const all = document.querySelectorAll('#content .mdp-search-hit');
    if (!all.length) return null;
    const vh = window.innerHeight;
    const center = vh / 2;
    let best = null, bestDist = Infinity;
    for (const el of all) {
      const r = el.getBoundingClientRect();
      if (r.bottom < 0 || r.top > vh) continue;
      const mid = (r.top + r.bottom) / 2;
      const d = Math.abs(mid - center);
      if (d < bestDist) { bestDist = d; best = el; }
    }
    if (best) return best;
    return document.querySelector('#content .mdp-search-hit.current') || all[0];
  }
  function mdpAskOpenFromSearchHit() {
    if (!ASK_ENABLED) return false;
    const term = (window.mdpSearchCurrentTerm || '').trim();
    if (!term) return false;
    const hit = mdpPickVisibleSearchHit();
    if (!hit) return false;
    document.querySelectorAll('#content .mdp-search-hit.current').forEach((m) => m.classList.remove('current'));
    hit.classList.add('current');
    try {
      const range = document.createRange();
      range.selectNodeContents(hit);
      const sel = window.getSelection();
      if (sel) { sel.removeAllRanges(); sel.addRange(range); }
      state.askSelectionText = term;
      state.askAnchorRect = range.getBoundingClientRect();
      mdpAskShowInputUI();
      return true;
    } catch (_) {
      return false;
    }
  }
  function mdpAskOpenInput() {
    if (!ASK_ENABLED || state.mode !== 'visual') return;
    const snap = mdpAskSnapshotSelection();
    if (!snap) { mdpSelectToast('no selection to ask about'); return; }
    if (!mdpAskServerReachable()) {
      if (mdpAskSidecarURL()) { mdpAskPromoteViaSidecar(snap.text); return; }
      mdpSelectToast('ask requires mdp watch or mdp serve');
      return;
    }
    state.askSelectionText = snap.text;
    state.askAnchorRect = snap.rect;
    mdpAskShowInputUI();
  }
  // mdpAskSelectTextInContent finds needle in #content and makes it
  // the native selection, returning true on hit. Tries window.find()
  // first (it walks across text nodes, handling selections that
  // crossed <a> or <code> inlines on the static page); falls back to
  // a single-text-node walk for browsers without window.find.
  function mdpAskSelectTextInContent(needle) {
    if (!needle) return false;
    const text = String(needle).trim().slice(0, 500);
    if (!text) return false;
    const content = document.getElementById('content');
    if (!content) return false;
    if (typeof window.find === 'function') {
      const sel = window.getSelection();
      if (sel) sel.removeAllRanges();
      try {
        if (window.find(text, false, false, false, false, false, false)) {
          const s = window.getSelection();
          if (s && s.rangeCount > 0 && content.contains(s.getRangeAt(0).startContainer)) {
            return true;
          }
        }
      } catch (_) {}
    }
    const walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT, null);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const val = n.nodeValue || '';
      const idx = val.indexOf(text);
      if (idx < 0) continue;
      try {
        const range = document.createRange();
        range.setStart(n, idx);
        range.setEnd(n, idx + text.length);
        const sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
        return true;
      } catch (_) { return false; }
    }
    return false;
  }
  // mdpAskFreezeSelection paints the live native selection as static
  // overlay rects so the highlight stays visible after focus shifts
  // to the ask input (which would otherwise blow away the native
  // selection range). When called with no live selection it leaves
  // existing frozen overlays in place — that's the static→watch
  // path, where the overlay is painted up-front and showInputUI's
  // re-call shouldn't clear it.
  function mdpAskFreezeSelection() {
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return;
    clearOverlays('frozen');
    const rects = sel.getRangeAt(0).getClientRects();
    for (const r of rects) {
      if (r.width <= 0 || r.height <= 0) continue;
      const span = document.createElement('span');
      span.className = 'mdp-select-frozen';
      span.dataset.mdpSelectKind = 'frozen';
      span.style.left = Math.round(r.left + window.scrollX) + 'px';
      span.style.top = Math.round(r.top + window.scrollY) + 'px';
      span.style.width = Math.round(r.width) + 'px';
      span.style.height = Math.round(r.height) + 'px';
      document.body.appendChild(span);
      state.overlays.push(span);
    }
  }
  function mdpAskShowInputUI(opts) {
    const isFollowup = !!(opts && opts.followup);
    if (state.askEl) { state.askEl.remove(); state.askEl = null; }
    if (state.askPillEl) { state.askPillEl.remove(); state.askPillEl = null; }
    if (state.askInputEl) { state.askInputEl.remove(); state.askInputEl = null; }
    mdpAskHideSelectionStar();
    mdpAskFreezeSelection();
    const wrap = document.createElement('div');
    wrap.className = 'mdp-ask-input';
    const input = document.createElement('input');
    input.type = 'text';
    input.spellcheck = false;
    input.autocomplete = 'off';
    input.placeholder = isFollowup ? 'ask a follow-up...' : 'ask claude about the selection...';
    input.value = isFollowup ? '' : (state.askLastPrompt || '');
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
        if (v) mdpAskSubmit(v, isFollowup);
      } else if (ev.key === 'Escape') {
        ev.preventDefault();
        mdpAskCloseAll();
      }
    });
  }
  function mdpAskReask() {
    if (!ASK_ENABLED) return;
    if (state.askEl) { state.askEl.remove(); state.askEl = null; }
    mdpAskShowInputUI({followup: true});
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
  async function mdpAskSubmit(prompt, isFollowup) {
    const priorPrompt = isFollowup ? (state.askLastPrompt || '') : '';
    const priorResponse = isFollowup ? (state.askLastResponse || '') : '';
    state.askLastPrompt = prompt;
    mdpAskShowPill();
    const abort = new AbortController();
    state.askAbort = abort;
    try {
      const r = await fetch('/ask', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({
          selection: state.askSelectionText,
          prompt,
          prior_prompt: priorPrompt,
          prior_response: priorResponse
        }),
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
      state.askLastResponse = data.raw || '';
      // Cache the fresh answer locally so the next Shift+Tab can use
      // it without a round-trip. The server-side JSONL is still the
      // source of truth across sessions.
      if (Array.isArray(state.askHistoryCache)) {
        state.askHistoryCache.push({ prompt, html: data.html || '', ts: Math.floor(Date.now()/1000) });
        if (state.askHistoryCache.length > 50) state.askHistoryCache.shift();
      }
      state.askHistoryIdx = -1;
    } catch (err) {
      if (err && err.name === 'AbortError') return;
      if (state.askPillEl) { state.askPillEl.remove(); state.askPillEl = null; }
      mdpSelectToast('ask failed: ' + err);
    } finally {
      if (state.askAbort === abort) state.askAbort = null;
    }
  }
  // mdpAskHistoryLoad fetches the per-file history from the server
  // on demand. Cached in state.askHistoryCache for the lifetime of
  // the page (cleared on file switch by the WS reload handler).
  async function mdpAskHistoryLoad() {
    if (Array.isArray(state.askHistoryCache)) return state.askHistoryCache;
    if (state.askHistoryFetching) return state.askHistoryFetching;
    state.askHistoryFetching = (async () => {
      try {
        const r = await fetch('/ask/history');
        const data = await r.json().catch(() => ({entries: []}));
        // Server returns newest-first; flip so the array is
        // oldest→newest, matching the local cache convention used by
        // mdpAskSubmit (push appends the newest).
        const arr = (data.entries || []).slice().reverse();
        state.askHistoryCache = arr;
        return arr;
      } catch (_) {
        state.askHistoryCache = [];
        return [];
      } finally {
        state.askHistoryFetching = null;
      }
    })();
    return state.askHistoryFetching;
  }
  // mdpAskHistoryStep walks the saved-answer list: dir=-1 goes older,
  // dir=+1 goes newer. First call from no-card lands on the newest
  // entry; further -1 calls walk back; +1 past the newest closes.
  async function mdpAskHistoryStep(dir) {
    if (!mdpAskServerReachable()) return;
    const arr = await mdpAskHistoryLoad();
    if (!arr.length) { mdpSelectToast('no ask history yet'); return; }
    let idx = state.askHistoryIdx;
    if (idx < 0 || idx >= arr.length) idx = arr.length;
    idx += dir;
    if (idx < 0 || idx >= arr.length) { mdpAskCloseAll(); state.askHistoryIdx = -1; return; }
    state.askHistoryIdx = idx;
    state.askAnchorRect = null;
    mdpAskRenderCard(arr[idx].html);
  }
  function mdpAskRenderCard(html) {
    if (state.askEl) { state.askEl.remove(); state.askEl = null; }
    const card = document.createElement('div');
    card.className = 'mdp-ask-card';
    if (ASK_CARD_WIDTH > 0) card.style.maxWidth = ASK_CARD_WIDTH + 'px';
    if (ASK_CARD_HEIGHT > 0) card.style.maxHeight = ASK_CARD_HEIGHT + 'vh';
    const header = document.createElement('div');
    header.className = 'mdp-ask-card-header';
    const exitKey = key('close') || 'q';
    const askKey = key('ask_again') || 'a';
    const hint = document.createElement('span');
    hint.className = 'mdp-ask-card-hint';
    hint.appendChild(mdpAskMakeKeyChip(exitKey));
    hint.appendChild(document.createTextNode(' exit  '));
    hint.appendChild(mdpAskMakeKeyChip(askKey));
    hint.appendChild(document.createTextNode(' ask something else'));
    const close = document.createElement('button');
    close.className = 'mdp-ask-card-close';
    close.setAttribute('aria-label', 'Close');
    close.textContent = String.fromCharCode(215);
    close.addEventListener('click', () => mdpAskCloseAll());
    header.appendChild(hint);
    header.appendChild(close);
    const body = document.createElement('div');
    body.className = 'mdp-ask-card-body markdown-body';
    body.innerHTML = html;
    card.appendChild(header);
    card.appendChild(body);
    document.body.appendChild(card);
    mdpAskPositionCard(card, state.askAnchorRect);
    state.askEl = card;
  }
  function mdpAskMakeKeyChip(label) {
    const span = document.createElement('span');
    span.className = 'mdp-ask-card-key';
    span.textContent = label;
    return span;
  }
  function mdpAskPositionAnchored(el, rect, opts) {
    const w = opts.width, h = opts.height;
    if (!rect) {
      el.style.left = (window.scrollX + Math.max(8, (window.innerWidth - w) / 2)) + 'px';
      el.style.top = (window.scrollY + Math.max(8, (window.innerHeight - h) / 2)) + 'px';
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
      el.style.left = (window.scrollX + Math.max(margin, (window.innerWidth - w) / 2)) + 'px';
      el.style.top = (window.scrollY + Math.max(margin, (window.innerHeight - h) / 2)) + 'px';
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
    if (ASK_ENABLED && state.askEl && !e.ctrlKey && !e.metaKey && !e.altKey) {
      if (isKey(e, 'close')) {
        e.preventDefault();
        e.stopPropagation();
        mdpAskCloseAll();
        state.askHistoryIdx = -1;
        return;
      }
      if (isKey(e, 'ask_again')) {
        e.preventDefault();
        e.stopPropagation();
        mdpAskReask();
        return;
      }
    }
    // Shift+Tab walks the saved-answer history; plain Tab steps
    // forward but only when a card is already showing (so it doesn't
    // steal Tab from the file-tree toggle in the default case).
    if (ASK_ENABLED && e.key === 'Tab' && mdpAskServerReachable()) {
      if (e.shiftKey) {
        e.preventDefault();
        e.stopPropagation();
        mdpAskHistoryStep(-1);
        return;
      }
      if (state.askEl) {
        e.preventDefault();
        e.stopPropagation();
        mdpAskHistoryStep(+1);
        return;
      }
    }
    if (state.mode === 'idle' || state.mode === 'visual') {
      if (HOP_ENABLED && isKey(e, 'select_pick')) {
        e.preventDefault();
        mdpSelectStartPick();
        return;
      }
      if (VISUAL_ENABLED && isKey(e, 'select_visual')) {
        e.preventDefault();
        if (state.mode === 'visual') {
          mdpSelectBail();
        } else {
          mdpSelectStartCenter();
        }
        return;
      }
      if (state.mode === 'idle') {
        // Escape closes any open ask UI before falling through to
        // the visual-mode Escape handler that bails the selection.
        if (ASK_ENABLED && e.key === 'Escape' &&
            (state.askEl || state.askPillEl || state.askInputEl || state.askAbort)) {
          e.preventDefault();
          mdpAskCloseAll();
          state.askHistoryIdx = -1;
          return;
        }
        // A live mouse selection wins over the search-hit / whole-file
        // fallback so the user can highlight then press c to ask about
        // just that snippet.
        if (ASK_ENABLED && isKey(e, 'select_ask')) {
          const snap = mdpAskSnapshotSelection();
          if (snap) {
            e.preventDefault();
            if (mdpAskServerReachable()) {
              state.askSelectionText = snap.text;
              state.askAnchorRect = snap.rect;
              mdpAskShowInputUI();
            } else if (mdpAskSidecarURL()) {
              mdpAskPromoteViaSidecar(snap.text);
            }
            return;
          }
          if (mdpAskServerReachable()) {
            e.preventDefault();
            if (!mdpAskOpenFromSearchHit()) mdpAskOpenWholeFile();
          } else if (mdpAskSidecarURL()) {
            e.preventDefault();
            const term = (window.mdpSearchCurrentTerm || '').trim();
            mdpAskPromoteViaSidecar(term || undefined);
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
  // Pre-warm the sidecar's watch server so the click-to-AI redirect
  // doesn't pay the cold-start cost (~100–500 ms). Using <img> over
  // fetch because file:// pages can't fetch http://localhost without
  // CORS, but image requests work; we don't care that the response
  // isn't actually an image, only that the side-effect (sidecar
  // starting watch) happens.
  function mdpAskPrewarmSidecar() {
    if (mdpAskServerReachable()) return;
    const u = mdpAskSidecarURL();
    if (!u) return;
    try { new Image().src = u + '/promote?warm=1'; } catch (_) {}
  }
  if (ASK_ENABLED) {
    mdpAskInstallFab();
    mdpAskPrewarmSidecar();
    // The sidecar's /promote redirect appends ?open=ask so the user's
    // single click on the static-mode AI icon both promotes AND opens
    // the input on arrival. Query param (not a URL fragment) because
    // some Chromium/WebKit builds strip the fragment on cross-origin
    // 302 redirects.
    try {
      const params = new URLSearchParams(window.location.search);
      if (mdpAskServerReachable() && params.get('open') === 'ask') {
        const sel = params.get('sel') || '';
        const scrollFrac = parseFloat(params.get('scroll') || '0') || 0;
        params.delete('open');
        params.delete('sel');
        params.delete('scroll');
        const q = params.toString();
        const url = window.location.pathname + (q ? '?' + q : '') + window.location.hash;
        try { history.replaceState(null, '', url); } catch (_) {}
        // Paint the highlight immediately so there's no perceptible
        // gap between the static-page selection going away and the
        // watch page showing it. Opening the input itself still waits
        // for load so focus sticks.
        let scrollTarget = null;
        if (sel) {
          state.askSelectionText = sel;
          if (mdpAskSelectTextInContent(sel)) {
            const r = window.getSelection().getRangeAt(0).getBoundingClientRect();
            state.askAnchorRect = r;
            mdpAskFreezeSelection();
            const ns = window.getSelection();
            if (ns) ns.removeAllRanges();
            scrollTarget = Math.max(0, r.top + window.scrollY - window.innerHeight / 2);
          } else {
            state.askAnchorRect = null;
          }
        }
        // Selection wins for scroll restoration. Fall back to the
        // scroll fraction the static page reported so plain idle 'c'
        // (no selection) still lands the user where they were.
        const restoreScroll = () => {
          if (scrollTarget != null) {
            window.scrollTo({ top: scrollTarget });
          } else if (scrollFrac > 0) {
            const max = Math.max(0, document.documentElement.scrollHeight - window.innerHeight);
            window.scrollTo({ top: max * scrollFrac });
          }
        };
        restoreScroll();
        const openAndFocus = () => {
          restoreScroll();
          if (sel) {
            mdpAskShowInputUI();
          } else {
            mdpAskOpenWholeFile();
          }
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

const defaultAskCardWidth = 560
const defaultAskCardHeight = 50

func buildSelectScript(keys KeyBindings, hop, visual, ask bool, askCardWidth, askCardHeight int) string {
	if !hop && !visual {
		return ""
	}
	if askCardWidth <= 0 {
		askCardWidth = defaultAskCardWidth
	}
	if askCardHeight <= 0 || askCardHeight > 100 {
		askCardHeight = defaultAskCardHeight
	}
	s := strings.ReplaceAll(selectScriptTemplate, "__HOP_ENABLED__", fmt.Sprintf("%t", hop))
	s = strings.ReplaceAll(s, "__VISUAL_ENABLED__", fmt.Sprintf("%t", visual))
	s = strings.ReplaceAll(s, "__ASK_ENABLED__", fmt.Sprintf("%t", ask && visual))
	s = strings.ReplaceAll(s, "__SELECT_KEYS_JSON__", keysJSON(keys))
	s = strings.ReplaceAll(s, "__ASK_CARD_WIDTH__", fmt.Sprintf("%d", askCardWidth))
	s = strings.ReplaceAll(s, "__ASK_CARD_HEIGHT__", fmt.Sprintf("%d", askCardHeight))
	return s
}

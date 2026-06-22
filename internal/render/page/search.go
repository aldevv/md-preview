package page

import "strings"

const searchScriptTemplate = `
(() => {
  const OPEN_KEY = __SEARCH_OPEN__;
  const NEXT_KEY = __SEARCH_NEXT__;
  const PREV_KEY = __SEARCH_PREV__;
  const MAX_HITS = 5000;
  let isOpen = false;
  let hits = [];
  let idx = -1;
  let term = '';
  window.mdpSearchIsOpen = false;
  window.mdpSearchCurrentTerm = '';

  function panel() { return document.getElementById('mdp-search'); }
  function input() { return document.getElementById('mdp-search-input'); }
  function counter() { return document.getElementById('mdp-search-count'); }

  function clearHits() {
    const content = document.getElementById('content');
    if (!content) return;
    const marks = content.querySelectorAll('mark.mdp-search-hit');
    marks.forEach((m) => {
      const parent = m.parentNode;
      while (m.firstChild) parent.insertBefore(m.firstChild, m);
      parent.removeChild(m);
      parent.normalize();
    });
    hits = [];
    idx = -1;
  }

  function paintHits(needle) {
    clearHits();
    term = needle || '';
    window.mdpSearchCurrentTerm = term;
    if (!term) { updateCounter(); return; }
    const content = document.getElementById('content');
    if (!content) return;
    const lower = term.toLowerCase();
    const walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT, {
      acceptNode(node) {
        if (!node.nodeValue) return NodeFilter.FILTER_REJECT;
        const parent = node.parentElement;
        if (!parent) return NodeFilter.FILTER_REJECT;
        if (parent.closest('script,style')) return NodeFilter.FILTER_REJECT;
        if (parent.classList && parent.classList.contains('mdp-search-hit')) return NodeFilter.FILTER_REJECT;
        return NodeFilter.FILTER_ACCEPT;
      }
    });
    const targets = [];
    for (let n = walker.nextNode(); n; n = walker.nextNode()) targets.push(n);
    for (const node of targets) {
      const text = node.nodeValue;
      const haystack = text.toLowerCase();
      let from = 0;
      const ranges = [];
      while (from <= haystack.length) {
        const at = haystack.indexOf(lower, from);
        if (at < 0) break;
        ranges.push([at, at + term.length]);
        from = at + Math.max(1, term.length);
        if (hits.length + ranges.length > MAX_HITS) break;
      }
      if (!ranges.length) continue;
      const parent = node.parentNode;
      let cursor = 0;
      const frag = document.createDocumentFragment();
      for (const [start, end] of ranges) {
        if (start > cursor) frag.appendChild(document.createTextNode(text.slice(cursor, start)));
        const mark = document.createElement('mark');
        mark.className = 'mdp-search-hit';
        mark.textContent = text.slice(start, end);
        frag.appendChild(mark);
        hits.push(mark);
        cursor = end;
      }
      if (cursor < text.length) frag.appendChild(document.createTextNode(text.slice(cursor)));
      parent.replaceChild(frag, node);
      if (hits.length >= MAX_HITS) break;
    }
    if (hits.length) {
      idx = 0;
      focusHit();
    } else {
      idx = -1;
    }
    updateCounter();
  }

  function focusHit() {
    hits.forEach((m, i) => m.classList.toggle('current', i === idx));
    const el = hits[idx];
    if (el && el.scrollIntoView) el.scrollIntoView({ block: 'center' });
  }

  function step(delta) {
    if (!hits.length) return;
    idx = (idx + delta + hits.length) % hits.length;
    focusHit();
    updateCounter();
  }

  function updateCounter() {
    const c = counter();
    if (!c) return;
    if (!term) { c.textContent = ''; return; }
    if (!hits.length) { c.textContent = '0/0'; return; }
    c.textContent = (idx + 1) + '/' + hits.length;
  }

  function open() {
    const p = panel(), inp = input();
    if (!p || !inp) return;
    p.hidden = false;
    isOpen = true;
    window.mdpSearchIsOpen = true;
    inp.focus();
    inp.select();
  }

  function close(keepHits) {
    const p = panel();
    if (p) p.hidden = true;
    isOpen = false;
    window.mdpSearchIsOpen = false;
    if (!keepHits) {
      clearHits();
      term = '';
      window.mdpSearchCurrentTerm = '';
      updateCounter();
    }
  }
  window.mdpSearchClear = () => { clearHits(); term = ''; window.mdpSearchCurrentTerm = ''; updateCounter(); };

  function blockedByOverlay() {
    return (typeof mdpFinderIsOpen !== 'undefined' && mdpFinderIsOpen) ||
      (typeof mdpTreeIsOpen !== 'undefined' && mdpTreeIsOpen) ||
      (typeof window.mdpSelectIsActive !== 'undefined' && window.mdpSelectIsActive);
  }
  function isEditable(el) {
    if (!el) return false;
    const tag = el.tagName;
    return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable;
  }

  document.addEventListener('keydown', (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    if (isEditable(e.target)) return;
    if (blockedByOverlay()) return;
    if (OPEN_KEY && e.key === OPEN_KEY) {
      e.preventDefault();
      open();
      return;
    }
    if (hits.length) {
      if (NEXT_KEY && e.key === NEXT_KEY) { e.preventDefault(); step(1); return; }
      if (PREV_KEY && e.key === PREV_KEY) { e.preventDefault(); step(-1); return; }
    }
  });

  (function wire() {
    const inp = input();
    if (!inp) return;
    inp.addEventListener('input', () => paintHits(inp.value));
    inp.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') { e.preventDefault(); close(false); return; }
      if (e.key === 'Enter') {
        e.preventDefault();
        if (e.shiftKey) step(-1); else step(1);
        close(true);
        return;
      }
      if (e.key === 'ArrowDown' || (e.ctrlKey && (e.key === 'n' || e.key === 'N'))) {
        e.preventDefault(); step(1); return;
      }
      if (e.key === 'ArrowUp' || (e.ctrlKey && (e.key === 'p' || e.key === 'P'))) {
        e.preventDefault(); step(-1); return;
      }
    });
  })();
})();
`

func buildSearchScript(keys KeyBindings) string {
	s := strings.ReplaceAll(searchScriptTemplate, "__SEARCH_OPEN__", jsString(keys["search_open"]))
	s = strings.ReplaceAll(s, "__SEARCH_NEXT__", jsString(keys["search_next"]))
	s = strings.ReplaceAll(s, "__SEARCH_PREV__", jsString(keys["search_prev"]))
	return s
}

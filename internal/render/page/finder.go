package page

import "strings"

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

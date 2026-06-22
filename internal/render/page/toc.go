package page

import "strings"

const tocScriptTemplate = `
let mdpTocIsOpen = false;
window.mdpTocIsOpen = false;
let mdpTocActiveId = '';

function mdpTocSlug(text) {
  return (text || '')
    .toLowerCase()
    .replace(/[^\w\s-]/g, '')
    .trim()
    .replace(/\s+/g, '-') || 'section';
}

function mdpBuildToc() {
  const root = document.getElementById('content');
  const wrapper = document.createElement('div');
  if (!root) {
    const empty = document.createElement('div');
    empty.className = 'mdp-toc-empty';
    empty.textContent = 'No content.';
    wrapper.appendChild(empty);
    return wrapper;
  }
  const headings = Array.from(root.querySelectorAll('h1, h2, h3, h4, h5, h6'));
  if (!headings.length) {
    const empty = document.createElement('div');
    empty.className = 'mdp-toc-empty';
    empty.textContent = 'No headings.';
    wrapper.appendChild(empty);
    return wrapper;
  }
  const usedIds = new Set();
  const ul = document.createElement('ul');
  ul.className = 'mdp-toc-list';
  for (const h of headings) {
    if (!h.id) {
      let base = mdpTocSlug(h.textContent);
      let id = base, i = 2;
      while (document.getElementById(id) || usedIds.has(id)) { id = base + '-' + (i++); }
      h.id = id;
    }
    usedIds.add(h.id);
    const li = document.createElement('li');
    li.className = 'mdp-toc-item mdp-toc-level-' + h.tagName.toLowerCase();
    const a = document.createElement('a');
    a.textContent = h.textContent;
    a.href = '#' + h.id;
    a.dataset.mdpTocId = h.id;
    a.addEventListener('click', (ev) => {
      ev.preventDefault();
      mdpTocSetActive(a);
      mdpTocJump(h.id);
      mdpToggleToc(false);
    });
    li.appendChild(a);
    ul.appendChild(li);
  }
  wrapper.appendChild(ul);
  return wrapper;
}

function mdpTocVisibleItems() {
  const panel = document.getElementById('mdp-toc');
  if (!panel || panel.hidden) return [];
  return Array.from(panel.querySelectorAll('.mdp-toc-item > a')).filter((el) => el.offsetParent !== null);
}

function mdpTocSetActive(el) {
  if (!el) return;
  document.querySelectorAll('#mdp-toc .active').forEach((n) => n.classList.remove('active'));
  el.classList.add('active');
  mdpTocActiveId = el.dataset.mdpTocId || '';
  if (el.scrollIntoView) el.scrollIntoView({ block: 'nearest' });
}

function mdpTocMove(delta) {
  const items = mdpTocVisibleItems();
  if (!items.length) return;
  const current = document.querySelector('#mdp-toc .active');
  const idx = Math.max(0, items.indexOf(current));
  mdpTocSetActive(items[(idx + delta + items.length) % items.length]);
}

function mdpTocJump(id) {
  const target = document.getElementById(id);
  if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' });
}

function mdpTocJumpActive(keepOpen) {
  const current = document.querySelector('#mdp-toc .active');
  if (!current) return;
  mdpTocJump(current.dataset.mdpTocId);
  if (!keepOpen) mdpToggleToc(false);
}

function mdpToggleToc(force) {
  const panel = document.getElementById('mdp-toc');
  if (!panel) return;
  const want = (force === true || force === false) ? force : !mdpTocIsOpen;
  if (want) {
    if (typeof mdpToggleTree === 'function' && window.mdpTreeIsOpen) mdpToggleTree(false);
    const body = panel.querySelector('.mdp-toc-body');
    body.innerHTML = '';
    body.appendChild(mdpBuildToc());
    panel.hidden = false;
    mdpTocIsOpen = true;
    window.mdpTocIsOpen = true;
    const items = mdpTocVisibleItems();
    if (items.length) {
      let pick = null;
      if (mdpTocActiveId) pick = items.find((el) => el.dataset.mdpTocId === mdpTocActiveId);
      mdpTocSetActive(pick || items[0]);
    }
  } else {
    panel.hidden = true;
    mdpTocIsOpen = false;
    window.mdpTocIsOpen = false;
  }
}
window.mdpToggleToc = mdpToggleToc;

(function () {
  const closeBtn = document.querySelector('#mdp-toc .mdp-toc-close');
  if (closeBtn) closeBtn.addEventListener('click', () => mdpToggleToc(false));
})();

document.addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  const tag = (e.target && e.target.tagName) || '';
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
  if (e.target && e.target.isContentEditable) return;
  if (window.mdpSelectIsActive) return;
  if (!mdpTocIsOpen) return;
  const isDown = e.key === 'ArrowDown' || e.key === __TOC_DOWN__;
  const isUp = e.key === 'ArrowUp' || e.key === __TOC_UP__;
  if (isDown || isUp) {
    e.preventDefault();
    mdpTocMove(isDown ? 1 : -1);
    return;
  }
  if (e.key === 'Enter') {
    e.preventDefault();
    mdpTocJumpActive(false);
    return;
  }
  if (e.key === 'Tab' && !e.shiftKey) {
    e.preventDefault();
    mdpTocJumpActive(true);
    return;
  }
  if (e.key === 'Escape' || e.key === __TOC_CLOSE__) {
    e.preventDefault();
    mdpToggleToc(false);
    return;
  }
});
`

func buildTocScript(keys KeyBindings) string {
	down := keys["tree_down"]
	if down == "" {
		down = "j"
	}
	up := keys["tree_up"]
	if up == "" {
		up = "k"
	}
	s := strings.ReplaceAll(tocScriptTemplate, "__TOC_DOWN__", jsString(down))
	s = strings.ReplaceAll(s, "__TOC_UP__", jsString(up))
	s = strings.ReplaceAll(s, "__TOC_CLOSE__", jsString(keys["close"]))
	return s
}

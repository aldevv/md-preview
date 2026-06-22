package page

import (
	"fmt"
	"strings"
)

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

function mdpTreeOpenActive(keepOpen) {
  const current = document.querySelector('#mdp-tree .active');
  if (!current) return;
  if (current.dataset.mdpTreeKind === 'file') {
    mdpTreeNavigate(current.dataset.mdpTreePath, keepOpen ? {keepOpen: true} : undefined);
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
  if (sessionStorage.getItem('mdpTreeAutoOpen') === '1') {
    sessionStorage.removeItem('mdpTreeAutoOpen');
    setTimeout(() => { if (typeof mdpToggleTree === 'function') mdpToggleTree(true); }, 0);
  }
})();

document.addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  const tag = (e.target && e.target.tagName) || '';
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
  if (e.target && e.target.isContentEditable) return;
  if (window.mdpSelectIsActive) return;
  if (window.mdpTocIsOpen) return;
  if (e.key === __TREE_TOGGLE__ && !e.shiftKey) {
    e.preventDefault();
    if (mdpTreeIsOpen) mdpTreeOpenActive(true);
    else mdpToggleTree(true);
    return;
  }
  if (e.key === 'Escape' && mdpTreeIsOpen) {
    e.preventDefault();
    mdpToggleTree(false);
    return;
  }
  if (!mdpTreeIsOpen) return;
  if (e.key === __TREE_CLOSE__) {
    e.preventDefault();
    mdpToggleTree(false);
    return;
  }
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
	s = strings.ReplaceAll(s, "__TREE_CLOSE__", jsString(keys["close"]))
	return s
}

// finderScriptTemplate powers the Ctrl+P fuzzy file finder. Depends on

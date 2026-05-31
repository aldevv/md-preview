# Hop-style visual select for md-preview

## Context

The user wants a hop.nvim-like text picker inside the rendered markdown preview, so they can grab a region and copy it without reaching for the mouse. Today the only in-preview text-selection path is native Chrome click-drag, which is awkward inside a `chrome --app=` popup tiled next to nvim. The Lua plugin (md-preview.nvim) just spawns the renderer over stdin; the in-browser experience lives entirely in the Go repo `aldevv/md-preview`. This change lands there.

UX (v1, MVP only):
1. Two entry keys:
   - `s` (hop-pick): enters "pick char" mode. Next non-modifier key is the target char. Every visible occurrence inside `#content` gets a single-letter label (a..z, viewport-clipped, hard cap 26). User types the label; caret jumps; visual mode opens anchored at that char.
   - `v` (center-anchor): skips the hop step. Visual mode opens immediately, anchored at the caret-equivalent of the viewport center via `document.caretRangeFromPoint(innerWidth/2, innerHeight/2)` (with `caretPositionFromPoint` shim for Safari).
2. In visual mode:
   - `h`/`l` (colemak `h`/`i`) extend the `Range` one character at a time.
   - Source line numbers appear as a transient left-margin gutter, one number per block element that has a `data-line` attribute (the same attrs the scroll-sync already populates, render.go:28-30). Helps orient the user without leaving the page.
   - `y` writes `selection.toString()` to the clipboard, toasts, exits.
   - `Esc` exits without yanking.
3. More than 26 hop matches: toast "too many, scroll closer" and bail (bigrams ship in v2).

Scope, default, and trigger confirmed with user: MVP only, on by default, hop-pick trigger `s`, center-anchor trigger `v`, line numbers shown in visual mode.

The two entry points are gated by **independent config flags**: `hop` (the `s` char picker) and `visual` (the `v` center-anchor + the visual-mode state machine itself). Both default on. Combinations:
- `hop=true, visual=true` — full feature; `s` ends in visual mode, `v` enters directly.
- `hop=true, visual=false` — `s` picks → labels → jump, selection becomes a single-char native browser selection, mode returns to idle. User copies with Ctrl+C. No vim motions / yank shortcut. `v` does nothing.
- `hop=false, visual=true` — `v` enters visual mode at viewport center with motions + `y`. `s` does nothing.
- both false — no entry points; renderer behaves as today.

## Files touched

All under `/home/kanon/repos/github.com/aldevv/md-preview`.

### 1. `internal/config/config.go`
- `Config` struct (lines 22–31): add two fields after `FuzzyFinder`:
  - `Hop bool \`toml:"hop"\``
  - `Visual bool \`toml:"visual"\``
- `defaultConfigTemplate` (lines 50–60): add two commented lines:
  - `# hop    = true   # 's' enters a hop.nvim-style char picker that jumps the caret`
  - `# visual = true   # 'v' enters visual mode at the viewport center; hjkl extend, 'y' yanks`
- `defaults()` (line 99–101): extend literal to `Config{FileTree: true, FuzzyFinder: true, Hop: true, Visual: true}`.
- `config_test.go`: add default-on assertions for both, mirroring the existing `FileTree`/`FuzzyFinder` cases.

### 2. `internal/render/page.go`
- After `finderScriptTemplate` (ends line 418): introduce `selectScriptTemplate` constant containing the full state machine. Inside the script, gate the two entry handlers on JS-level booleans (`__HOP_ENABLED__`, `__VISUAL_ENABLED__`) that get substituted to `true`/`false` so the keydown dispatcher knows which entry keys to claim. The visual-mode state machine functions are always present in the script body; only the entry listeners are gated.
- Use the same `__DOWN__`/`__UP__`/`__RIGHT__` colemak placeholders the existing `vimKeys()` (lines 80–95) uses; add a sibling helper `selectKeys(colemak, hop, visual bool) string` that runs the same colemak substitution and additionally rewrites the two enable flags.
- `BuildPage` (line 784): append two positional params (after `staticTreeJSON`): `hop bool`, `visual bool`. Inside, near the existing tree/finder gating around line 820: `selectScript := ""; if hop || visual { selectScript = selectKeys(colemak, hop, visual) }`. Emitting nothing when both are off keeps the page byte-identical to today for users who disable both.
- `pageTemplate` (lines 571–773): add a new `__VISUAL_SELECT_SCRIPT__` placeholder between `__FINDER_SCRIPT__` and `__WS_SCRIPT__` (around line 769–770), so the select handler registers AFTER tree/finder and can guard with `typeof mdpFinderIsOpen !== 'undefined' && mdpFinderIsOpen`.
- `strings.NewReplacer` (lines 842–866): add `"__VISUAL_SELECT_SCRIPT__", selectScript,` entry.

### 3. Callers of `BuildPage` (signature change)
Three production sites and ~22 test sites need TWO extra positional args (`hop`, `visual`). Verified via `grep -rn "BuildPage(" --include="*.go"`:
- `internal/server/server.go:274` — pass `s.hop, s.visual`. Add two fields to the server struct (near the existing `fileTree`/`fuzzyFinder` fields) and propagate through its constructor / options struct.
- `internal/render/static_tree.go:36` (`Options` struct): add `Hop bool` and `Visual bool`. Line 168: pass `opts.Hop, opts.Visual`.
- `cmd/mdp/main.go:417`: pass `rc.cfg.Hop, rc.cfg.Visual` alongside `rc.cfg.Colemak`.
- `internal/render/render_test.go`: ~22 `BuildPage(...)` calls — mechanical sweep, append two trailing `false, false` (lines 429, 448, 466, 475, 484, 491, 499, 515, 521, 530, 540, 546, 554, 569, 584, 606, 619, 640, 649, 667, 679, 691).

### 4. `internal/render/assets/css/chrome.css`
Append a block mirroring the existing `#mdp-finder` styling (around lines 192–246):
- `.mdp-select-label` — absolute-positioned span, `z-index: 200` (above tree 50 / finder 100), `pointer-events: none`, monospace, padding `0 2px`, `border-radius: 2px`, `font-weight: bold`, `font-size: 12px`, bg `var(--color-select-label-bg)`, fg `var(--color-bg-primary)`.
- `.mdp-select-linenum` — absolute-positioned span pinned to the left edge of the viewport, `z-index: 199` (just under labels), `pointer-events: none`, monospace, `font-size: 11px`, `opacity: 0.6`, fg `var(--color-text-secondary)`. One per visible `[data-line]` block while visual mode is active. Recomputed on scroll/resize via a `requestAnimationFrame`-throttled handler.
- `body.mdp-select-mode #content ::selection` — explicit highlight color so the native selection stays visible in `chrome --app=` when the WM steals focus. Use `background: var(--color-select-selection)`.
- Add `--color-select-label-bg` and `--color-select-selection` defaults to whichever theme files set the CSS variables today (`theme-dark.css`, `theme-light.css` if they exist; otherwise the `:root` block in `chrome.css`).

### 5. Go-side tests (`internal/render/render_test.go`)
Mirror the existing `TestBuildPage_FuzzyFinderOmittedByDefault` shape (line 639):
- `TestBuildPage_HopOnly` — `hop=true, visual=false`. Assert the hop entry (`'s'` listener tokens, `mdpSelectStartPick`) is emitted and the visual entry (`'v'`, `mdpSelectStartCenter`) is NOT.
- `TestBuildPage_VisualOnly` — `hop=false, visual=true`. Inverse assertion.
- `TestBuildPage_HopAndVisual` — both true. Assert both entry handlers + the line-number paint function are present.
- `TestBuildPage_SelectModeOmitted` — both false. Assert none of the `mdpSelect*` tokens appear and the script placeholder collapses to empty.
- One assertion in `internal/server/server_test.go` near the existing finder check: with `Hop: true, Visual: true`, the response body contains the script.

## JS architecture for `selectScriptTemplate`

IIFE-wrapped, self-contained, reuses the `isEditable()` pattern from `vimKeysScriptTemplate` (page.go:19–23). State held in one `mdpSelectState = { mode: 'idle' | 'pickChar' | 'labeled' | 'visual', anchor: {node, offset}, head: {node, offset}, labels: Map<string, {node, offset}>, overlayEls: [] }`.

Functions:
1. `mdpSelectStartPick()` — enters `pickChar`, adds `body.mdp-select-mode`, installs a one-shot capturing keydown.
2. `mdpSelectStartCenter()` — entry for `v`. Calls `document.caretRangeFromPoint(innerWidth/2, innerHeight/2)` (Safari shim: `document.caretPositionFromPoint`). If the result is non-null and inside `#content`, transition straight to `mdpSelectJumpTo(range.startContainer, range.startOffset)`. If null (covered by an absolutely-positioned overlay or outside content), fall back to the nearest text node found via `elementsFromPoint`.
3. `mdpSelectBail()` — strip label and line-number overlays, clear selection, drop the class, remove listeners, reset to idle.
4. `mdpSelectVisibleTextNodes()` — `TreeWalker(SHOW_TEXT)` rooted at `#content`, parent must not be inside `script`/`style`. Reject nodes whose `parentElement.getBoundingClientRect()` is outside `[-200, innerHeight+200]`.
5. `mdpSelectPaintLabels(char)` — iterate walker, for each char-match build a one-shot `Range` set to that char offset, call `getBoundingClientRect()`, position a `<span class="mdp-select-label">` (parented to `document.body`, not `#content`, to avoid relayout) at `rect.left + scrollX`, `rect.top + scrollY`. Cap at 26 hits; if >26, toast and bail.
6. `mdpSelectPaintLineNumbers()` — called on enter-visual and on scroll (rAF-throttled). `document.querySelectorAll('#content [data-line]')` filtered to viewport via `getBoundingClientRect()`. For each, create `<span class="mdp-select-linenum">` parented to `document.body`, positioned at `left: 4px; top: rect.top + scrollY`, text = `data-line` value. Removed by `mdpSelectBail`.
7. `mdpSelectJumpTo(node, offset)` — transition to `visual`, anchor=head={node, offset}, build a Range, install via `getSelection().removeAllRanges(); addRange(range)`, tear down hop labels, call `mdpSelectPaintLineNumbers()`, attach scroll listener.
8. `mdpSelectMoveChar(delta)` — `head.offset += delta`. If offset goes negative or past `node.length`, walk the TreeWalker to the adjacent text node. Then `mdpSelectRebuildRange()`.
9. `mdpSelectRebuildRange()` — recompute `Range` start/end from `anchor` and `head`, swap if `head` precedes `anchor` (compare via `node.compareDocumentPosition`).
10. `mdpSelectYank()` — `await navigator.clipboard.writeText(getSelection().toString())`, then `mdpShowToast('yanked ' + n + ' chars')` (helper exists at page.go:693), then `mdpSelectBail()`.
11. Keydown dispatcher: switch on `mdpSelectState.mode`. Always check `isEditable(e.target)` and `if (typeof mdpFinderIsOpen !== 'undefined' && mdpFinderIsOpen) return;` (same for tree). Both `s` and `v` are entry points dispatched from the idle branch. All listeners must `preventDefault()` keys they consume so vimKeys doesn't fire.

Colemak handling stays Go-side via `selectKeys(colemak)` rewriting `__DOWN__`/`__UP__`/`__RIGHT__` — same approach `vimKeys()` already uses. Labels themselves are layout-stable in colemak so no per-key remap there.

## Risks and decisions deferred to v2

1. **Clipboard in `file://` static mode.** `navigator.clipboard.writeText` requires a secure context; Firefox blocks it under `file://`. v1 documents "localhost-mode only"; v2 adds the `textarea + execCommand('copy')` fallback.
2. **>26 visible matches.** v1 bails with a toast. v2 adds bigram labels (aa, ab, …) with a home-row-clustered seq.
3. **Visual-line vs block-line for j/k.** Not in MVP. v2 picks `document.caretRangeFromPoint` for true visual lines and writes a `caretPositionFromPoint` shim for Safari.
4. **KaTeX/Mermaid subtrees** pollute labels with annotation text. v1 has no math-heavy users on the user's machine; v2 skips `.katex`, `.mermaid` subtrees in the walker.
5. **Trigger collision.** `s` and `v` are both unbound today. Listener must register at capture phase so it preempts any future user-added bindings.
6. **Line-number overlay performance on scroll.** Visual mode adds a scroll listener that repaints line numbers; throttle with `requestAnimationFrame` and reuse existing overlay spans rather than recreating them per frame. On 1000-line documents only ~30-40 are visible at once, so element count stays trivial.
7. **Center-anchor on an empty viewport.** If `caretRangeFromPoint(innerWidth/2, innerHeight/2)` lands on padding or a non-text element (heading margin, code-block gutter), the fallback walks `elementsFromPoint` for the nearest descendant with text content. If still nothing, toast "no text at viewport center" and stay idle.

## Verification

Manual (required for "ship"):
1. `mdp README.md` in WS mode. Press `s`, then `e`. Labels appear on every visible `e`. Press `a` — caret jumps. Press `l` three times — selection extends right. Press `y`. Toast confirms. Paste into a terminal — verify content.
2. `mdp -e README.md` (static `file://` mode). Same flow. If clipboard write fails, the toast must say so explicitly (v1 is allowed to fail here).
3. `MDP_COLEMAK=1 mdp README.md`. Press `s`, target a char, then `i` should extend right in visual mode. `s` itself unchanged.
4. Open with `Ctrl+P` first. Press `s`. Finder should swallow it; select mode must NOT activate.
5. Long-document smoke: render a 1000-line `.md`, trigger `s e`, confirm labels appear only on visible matches (no offscreen overlays leaking) and label paint feels instant.

Automated:
- The three Go tests in §5 above. Run via `make test` in the mdp repo.
- Lua plugin tests (`make test-lua` in md-preview.nvim) should still pass — this change is renderer-only.

## Effort

~1 day for MVP. Most of the time is mechanical: signature sweep across ~25 `BuildPage` callsites, then writing/iterating the `selectScriptTemplate` JS and the chrome.css styles. The vim-motion design work is already done in this plan, so implementation is wiring.

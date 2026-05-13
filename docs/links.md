# Cross-format link navigation

Mdp's preview should treat the directory of the open document as a
miniature site: clicking `[design](design.tex)` from inside a
rendered README should render `design.tex` in the same window, with
the back button returning to the README. Today's preview only
intercepts `<a>` clicks that don't go anywhere useful; this doc
plans the click-to-navigate behaviour and the error UX for clicks
that can't be honoured.

## Scope

In scope:

- Any local-path link to a file mdp can render. That means `.md` (via
  goldmark) plus every extension `pandoc.InputFormat` recognises
  (`.tex`, `.rst`, `.org`, `.docx`, `.odt`, `.epub`, `.html`, …).
- Path confinement to the served directory (the `state.fileDir`
  that the server already enforces on `/render`).
- A clean error UX when the click can't be honoured: file missing,
  extension not renderable, path outside the served directory.

Out of scope (for the first cut):

- Static mode (`mdp foo.md`) link navigation. Static mode has no
  server to re-render on click; covered as Phase 2 below.
- `\href{}` rewriting inside pandoc-rendered HTML. Pandoc emits
  `<a href="…">` with whatever the source file said; the same
  click-handler covers it (no extra plumbing needed) as long as the
  link is a local path.
- Anchor links (`#section`). Browser default behaviour, untouched.
- External links (`https:`, `mailto:`, …). Browser default.

## Phase 1: serve / watch (WS-backed)

These modes have a long-lived server, so clicks become a POST to
`/render` with the resolved path. The existing WS `reload` broadcast
already carries the new file path, so all open clients re-sync
without extra plumbing.

### Server-side

In `internal/server/server.go::handleRender`, after resolving the
`file=` override to an absolute path and confirming it's inside
`state.fileDir`, add two new responses before the render runs:

1. `404 {"error": "file not found: <path>"}` when `os.Stat(abs)`
   fails (or the entry is a directory).
2. `415 {"error": "unsupported format: <ext>"}` when the extension is
   neither `.md`/`.markdown` nor recognised by
   `pandoc.InputFormat(abs)`.

The existing `403 path outside served directory` stays.

Successful renders keep the current contract: server broadcasts the
reload via WS, returns `200 {"ok": true, "version": N}`.

### Client-side

The page template (`internal/render/page.go`) gets three small
additions:

1. `<meta name="mdp-current-file" content="<abs path>">` so JS knows
   where to resolve relative hrefs from. Updated by the existing WS
   `reload` handler (which already receives `msg.file`).
2. A hidden toast element + ~10 lines of CSS:

   ```html
   <div id="mdp-toast" hidden></div>
   ```

   Fixed bottom-right, fade-in on `.visible`, auto-hides after 3 s.
3. A click handler attached to `#content` that runs per
   `<a>`-targeted click:

   - Skip when `href` starts with `#` (anchor) or matches
     `^[a-z]+:` (external scheme).
   - Resolve relative hrefs against `dirname(currentFile)`.
   - `e.preventDefault()` and POST `{file: <resolved>}` to
     `/render`.
   - On `2xx`: do nothing. The WS reload broadcast swaps the page.
   - On `4xx`: parse `{"error": "..."}` from the body and call
     `mdpShowToast(error)`. No `pushState`, no page swap.

### Extension authority

The server decides what's renderable, not the client. The client
posts any local-path href; the server returns `415` if the extension
isn't in `pandoc.InputFormat`. Single source of truth, no client
allowlist to drift out of sync.

### Tests

- `internal/server/server_test.go`: three new cases on POST
  `/render`: nonexistent file → `404 + {"error":...}`, `.xyz` →
  `415`, out-of-tree → `403` (existing coverage).
- JS click handler isn't covered by automated tests; manual smoke in
  chrome `--app=` window. A future `tests/smoke.js` Playwright run
  could exercise it if the existing e2e harness comes back.

## Phase 2: static mode (deferred)

`mdp foo.md` writes a single HTML file and points the browser at
`file://` it. There is no server to ask. Two viable approaches when
we get here:

a. **Pre-render the link graph.** BFS through `filepath.Dir(entry)`
   following any link whose target extension is renderable. Write
   each to `/tmp/mdp-<sha>.html`; rewrite hrefs in the source HTML
   to point at the new files. Cap at e.g. 200 files; over-cap leaves
   the remaining links untouched.

b. **Static-mode error sentinel.** Replace local-path hrefs with
   `javascript:mdpShowToast("not available in static mode; run mdp
   watch to navigate")` so clicks at least produce a clean message
   rather than a raw-source view.

Suggested mix: pre-render `.md` (cheap, ~13 ms each in goldmark)
and use the sentinel for non-md formats (pandoc subprocess per
linked `.docx` adds up quickly). Revisit if usage warrants the
extra wiring.

## Confinement and security

- Path confinement (`pathInsideDir` on the absolute resolved path)
  already gates `/render`; nothing changes there.
- `--sandbox` on pandoc renders still applies, so a malicious
  `.tex` linked from a README can't exfiltrate via `\input{}` or
  `\includegraphics{}`.
- The toast displays server-supplied error strings; the server only
  emits its own `fmt.Sprintf` text (path or extension echoes
  included), so a hostile path string is the only injection vector.
  Render the toast text via `textContent` rather than `innerHTML` to
  prevent any XSS through path-name embedding.

## Open questions

- The toast auto-dismisses at 3 s. Manual dismiss (click-to-close)?
- Should clicks during a pending render queue (double-click on a
  link before the previous render completes)? Today's `/render` is
  serialised by `state.mu`, so a queued POST will wait its turn,
  which seems fine.

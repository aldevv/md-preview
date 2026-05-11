# mdp skill reference

Embedded reference for Claude Code skills (or any automation) driving `mdp`
programmatically. Loaded via `cat "$(mdp skill path)"`. Content is versioned
with the binary, so skill prose can stay generic and rely on this for the
binary-coupled details.

## Invocation modes

- `mdp <file>` (one-shot): renders `<file>` to an HTML tempfile and spawns
  the browser. `mdp` exits immediately; the preview is static (no live
  updates). Best for rendering a snapshot like a plan or a fixed note.
- `mdp watch <file>` (long-running): starts the preview server with a
  250ms mtime+size poll so the preview auto-refreshes on any external
  edit. Blocks until Ctrl-C. Best for live editing without an editor
  plugin. Skills that want auto-refresh should run this in a tmux
  window/pane, not inline.
- `mdp serve <file> <port> <theme>` (Neovim plugin only): communicates
  with the plugin over stdin newline-delimited JSON. Skills should not
  invoke this directly.

## Tempfile convention

When a skill writes markdown to a tempfile for rendering, use a stable
per-purpose path so re-runs overwrite rather than accumulating clutter:

    {TMPDIR}/mdp-<skill-name>-<purpose>.md

Example: a skill that previews Claude plans uses
`/tmp/mdp-claude-plan.md`. Re-invocations overwrite the same file; the
user reloads the browser tab.

## Spawn semantics

`mdp <file>` launches the browser as a detached process (new session) and
returns. The browser keeps running after `mdp` exits. Don't background
`mdp` yourself (`mdp ... &`); one-shot mode already returns immediately
and double-backgrounding only confuses process bookkeeping.

`mdp watch` does NOT detach; the watcher blocks the foreground.

## Concurrency

`mdp <file>` always spawns a fresh browser tab/window. There is no
persistent server between one-shot runs. For a single persistent preview
that reflects every edit, run `mdp watch <file>` once and keep it
running; the same URL stays valid for the file's lifetime.

## Security guard rails (FYI, no action needed)

- HTTP server is loopback-only; the `Host` header is verified loopback
  on every request, and `Origin` is verified loopback when the browser
  sends one (no-CORS requests with no `Origin` still pass the Host check
  and the loopback bind).
- JSON bodies on `/render` and `/scroll` are capped at 64 KiB
  (`http.MaxBytesReader`).
- `/render`'s optional `file` override is path-confined to the directory
  of the originally-served file (the stdin `render` IPC over the private
  pipe is privileged and not subject to that confinement).
- Raw HTML in markdown is intentionally not rendered. Don't ask for it
  via a flag, there isn't one, the sandboxing story isn't built.

Skills invoking `mdp <file>` benefit from these implicitly.

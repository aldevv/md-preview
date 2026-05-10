# md-preview

Standalone markdown preview CLI in Go. Renders `.md` files to HTML and opens them in a browser, or runs as a long-lived scroll-synced preview server (used by the [md-preview.nvim](https://github.com/aldevv/md-preview.nvim) Neovim plugin).

Single static binary. No Python, no pip, no venv.

## Install

One-liner — uses `go install` if Go is on PATH, otherwise downloads a prebuilt release binary:

```sh
curl -fsSL https://raw.githubusercontent.com/aldevv/md-preview/main/install.sh | sh
```

Override the prefix with `PREFIX=...` (default `$HOME/.local`):

```sh
PREFIX=/usr/local sh install.sh   # system-wide
```

Or directly with Go:

```sh
go install github.com/aldevv/md-preview/cmd/mdp@latest
```

## Usage

```sh
mdp                        # fzf-pick a .md from cwd, then preview
mdp README.md              # render + open in browser
mdp -e README.md           # preview AND open the file in nvim
mdp -e                     # fzf-pick, preview, and edit
mdp -t light README.md     # light theme
mdp -p README.md           # print HTML path, don't open browser
mdp help                   # show help
```

If `fzf` is missing and you run `mdp` with no file argument, the help text is printed (`mdp` itself integrates with fzf for the picker).

`-e` opens nvim after spawning the browser preview. The preview is static — re-run `mdp` to refresh, or use the Neovim plugin for live scroll-sync.

## Features

- GitHub-flavored markdown via [goldmark](https://github.com/yuin/goldmark): tables, strikethrough, autolink, task lists.
- Syntax highlighting via highlight.js (CDN).
- Dark / light themes, custom CSS overlay.
- Vim-style scroll bindings in the rendered page: `j`/`k`, `h`/`l`, `d`/`u` (half-page), `g`/`G` (top/bottom), `q` to close. Colemak users get `h`/`n`/`e`/`i` via `colemak = true`.
- YAML frontmatter is stripped, not rendered.
- Raw HTML in markdown is intentionally **not** rendered (security): the preview origin is loopback-bound and a malicious README could otherwise inject scripts.

## Config — `~/.config/md-preview/config.toml`

A commented template is seeded on first run. All keys optional:

```toml
theme      = "dark"          # "dark" or "light"
font_size  = 18              # body font-size in px
custom_css = "~/path.css"    # appended after defaults; cascade wins
browser    = "auto"          # "auto" | "firefox --new-window" | ["cmd", "arg"]
                             # The URL is appended as the last arg.
                             # auto = chrome --app= → xdg-open / open
edit       = false           # default for -e (also open nvim). Override with -e / --no-edit.
colemak    = false           # swap in-page nav keys j/k/l → n/e/i
```

CLI flags override config values.

## Server mode (used by md-preview.nvim)

```sh
mdp serve <file> <port> <theme>
```

Long-running HTTP+WebSocket server bound to `127.0.0.1:<port>`. Reads JSON commands on stdin (`render`, `scroll`, `quit`), broadcasts reload/scroll messages over WebSockets to the connected browser tab. The Neovim plugin spawns this and communicates via stdin.

Endpoints (all loopback-only; foreign Origin / Host headers are rejected):
- `GET /` — rendered HTML page
- `GET /reload` — current render version (used by the plugin's readiness probe)
- `GET /ws` — WebSocket upgrade for live reload + scroll sync
- `POST /render` — re-render (optionally switching `file`, restricted to the originally-served directory)
- `POST /scroll` — broadcast a scroll target line

## Requirements

- Go 1.26.2+ (only for building from source; release users don't need it).
- A browser. If `google-chrome` / `chromium` is on `PATH`, `mdp` opens it with `--app=` for a chromeless window; otherwise it falls back to `xdg-open` (Linux) or `open` (macOS). Override with `browser = ...` in the config.
- `fzf` is optional but recommended — required for the no-arg picker mode.

## Layout

```
cmd/mdp/main.go              -- CLI entrypoint + `mdp serve` subcommand
internal/render              -- markdown → HTML body + page template
internal/server              -- HTTP + WebSocket server for the plugin
internal/config              -- TOML config, browser detection, fzf picker
```

## License

MIT — see [LICENSE](LICENSE).

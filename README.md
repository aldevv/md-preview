# mdp — instant markdown preview in your browser

[![Latest release](https://img.shields.io/github/v/release/aldevv/md-preview)](https://github.com/aldevv/md-preview/releases)

Preview any `.md` file in a real browser tab — single static binary. Pairs with [md-preview.nvim](https://github.com/aldevv/md-preview.nvim) for live scroll-sync (the editor cursor tracks the rendered page).

https://github.com/user-attachments/assets/19f64fa1-a4d6-4a9c-a94f-2c40ca5a979b

## Install

> [!NOTE]
> Linux and macOS only (amd64 / arm64). Windows is not supported.

One-liner — uses `go install` if Go is on `PATH`, otherwise downloads a prebuilt release tarball:

```sh
curl -fsSL https://raw.githubusercontent.com/aldevv/md-preview/main/install.sh | sh
```

System-wide (default prefix is `$HOME/.local`):

```sh
curl -fsSL https://raw.githubusercontent.com/aldevv/md-preview/main/install.sh | PREFIX=/usr/local sh
```

Or directly with Go:

```sh
go install github.com/aldevv/md-preview/cmd/mdp@latest
```

- Prebuilt binaries — [Releases page](https://github.com/aldevv/md-preview/releases).
- Building from source needs Go 1.26.2+ (release users don't).

Verify the install:

```sh
mdp help
```

Update to the latest release in place (uses `go install` if Go is on `PATH`, otherwise downloads the matching release tarball and swaps the binary):

```sh
mdp update           # install latest
mdp update --check   # just report whether an update is available
```

`mdp` auto-detects a browser (Chromium- or Firefox-family, then `xdg-open` / `open`). Override via `browser = ...` in the [config](#config).

### Optional: `fzf`

[`fzf`](https://github.com/junegunn/fzf) is the fuzzy-finder used by the no-arg picker mode — `mdp` with no file argument fzf-picks a `.md` from the current directory. If `fzf` is missing, the help text is printed instead.

## Usage

```sh
mdp README.md                  # render + open in browser
mdp                            # fzf-pick a .md from cwd, then preview
mdp watch README.md            # auto-refresh when the file changes (any editor)
mdp -e README.md               # preview AND open the file in nvim
mdp -e                         # fzf-pick, preview, and edit
mdp -t light README.md         # light theme
mdp -p README.md               # print HTML path, don't open browser
mdp foo.tex                    # render LaTeX (auto-starts a preview server)
mdp serve README.md 8080 dark  # plugin server mode (used by md-preview.nvim)
mdp help                       # show help
```

`mdp watch` keeps `mdp` running and the browser refreshes whenever the file
is saved — editor-agnostic (works with VS Code, Sublime, vim, Helix, your
`$EDITOR`, anything that writes to disk). Ctrl-C to stop; the preview tab
closes with it (chrome `--app=` mode) or shows a "server stopped" notice.

### Keys

| Action            | Default   | Colemak   |
| ----------------- | --------- | --------- |
| Down / Up         | `j` / `k` | `n` / `e` |
| Left / Right      | `h` / `l` | `h` / `i` |
| Half-page down/up | `d` / `u` | `d` / `u` |
| Top / Bottom      | `g` / `G` | `g` / `G` |
| Close             | `q`       | `q`       |

Enable Colemak with `colemak = true` in the [config](#config).

### LaTeX

mdp renders `.tex` / `.latex` files and ` ```latex ` fenced blocks inside markdown. The Pandoc 3.9 wasm32-wasi build is bundled into the binary; the browser runs it via `@bjorn3/browser_wasi_shim`, no external `pandoc` install required. Output is DOMPurify-sanitized before injection.

Because the browser needs to load the WASM bundle, LaTeX previews always run through the auto-reload HTTP server — `mdp foo.tex` is equivalent to `mdp watch foo.tex` and stays running until Ctrl-C. Static-mode (`-p` print path) is not available for LaTeX content; use `mdp watch` instead.

Coverage matches Pandoc: sectioning, lists, refs, tables, common math, citations. TikZ/PGFPlots are dropped (no real TeX engine), `\input{}` across multiple files isn't resolved (the WASM filesystem is in-memory), and editor scroll-sync inside `.tex` previews is deferred. See [`docs/latex.md`](docs/latex.md) for the full design.

### Notes

The default preview is static — re-run `mdp` to refresh, use `mdp watch` for
auto-refresh on save (any editor), or install the
[Neovim plugin](#neovim-plugin) for live scroll-sync.

YAML frontmatter is stripped.

> [!IMPORTANT]
> Raw HTML in markdown is intentionally **not** rendered: the preview origin is loopback-bound and a malicious README could otherwise inject scripts.

## Config

Settings live in `~/.config/md-preview/config.toml`; a commented template is seeded on first run. All keys optional:

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

## Neovim plugin

For live scroll-sync — where the rendered page tracks your editor cursor as you scroll the source — install the sibling plugin:

- [aldevv/md-preview.nvim](https://github.com/aldevv/md-preview.nvim)

The plugin spawns `mdp serve <file> <port> <theme>` as a long-lived loopback HTTP+WebSocket server and drives it over stdin newline-delimited JSON (`render`, `scroll`, `quit`). Plugin authors and contributors: see [docs/server.md](docs/server.md) for the IPC contract, endpoints, and internal layout.

## Claude Code skill

If you use [Claude Code](https://claude.com/claude-code), there's a companion skill at [`general/.claude/skills/md-preview`](https://github.com/aldevv/dotfiles/tree/main/general/.claude/skills/md-preview) (in `aldevv/dotfiles`) that lets Claude open markdown content in `mdp` on demand. Trigger it with `/md-preview <path>` to render a file, or just say "open this in mdp" / "show me the README rendered" and Claude resolves the file or writes generated content to a tempfile, then spawns `mdp` on it. Per-invocation by design (no auto-trigger). Requires this binary on `$PATH`.

The skill loads its mdp-driving reference from the binary itself via `mdp skill path`, so the canonical guide on invocation modes, tempfile conventions, and spawn semantics ships with the release rather than the skill prose.

## Contributing

Issues and PRs welcome at [github.com/aldevv/md-preview/issues](https://github.com/aldevv/md-preview/issues).

```sh
make test         # go test ./...
make build        # produces ./mdp
make install      # go install ./cmd/mdp
make pandoc-wasm  # refresh the embedded pandoc.wasm.gz (when bumping PANDOC_WASM_VERSION)
make test-e2e     # browser end-to-end suite (needs Chromium, see below)
```

The repo ships `internal/render/latex/wasm/pandoc.wasm.gz` (16 MB, gzipped from upstream's 58 MB) so that `go install github.com/aldevv/md-preview/cmd/mdp@latest` builds cleanly without a separate fetch step. Browsers decode the gzip transparently during `WebAssembly.instantiateStreaming` via the `Content-Encoding: gzip` header mdp sends on the `/_/pandoc.wasm` route, so there's no runtime decompression cost. To upgrade it, bump `PANDOC_WASM_VERSION` in the Makefile, run `make pandoc-wasm`, then commit the new `pandoc.wasm.gz` + `pandoc.wasm.sha256` together.

### Browser e2e tests

`internal/server/latex_e2e_test.go` (build tag `e2e`) drives a headless Chromium via [playwright-go](https://github.com/playwright-community/playwright-go) against the production handler and asserts that pandoc.wasm initializes, `latex-render.js` swaps `.latex-pending` placeholders, KaTeX picks up math markers, and math-free markdown does NOT pull in the WASM bundle. Run them with `make test-e2e` after a one-time setup:

```sh
go install github.com/playwright-community/playwright-go/cmd/playwright@latest
playwright install --with-deps chromium
make test-e2e
```

The default `go test ./...` skips these (no `-tags=e2e`), so CI and local hacking stay hermetic. Run them before bumping `PANDOC_WASM_VERSION` or touching anything under `internal/render/latex/wasm/`.

## License

MIT — see [LICENSE](LICENSE).

# mdp — instant markdown preview in your browser

[![Latest release](https://img.shields.io/github/v/release/aldevv/md-preview)](https://github.com/aldevv/md-preview/releases)

Preview any `.md` file in a real browser tab — single static binary. Pairs with [md-preview.nvim](https://github.com/aldevv/md-preview.nvim) for live scroll-sync (the editor cursor tracks the rendered page).

https://github.com/aldevv/md-preview/raw/main/docs/demo.mp4

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

`mdp` auto-detects a browser (Chromium- or Firefox-family, then `xdg-open` / `open`). Override via `browser = ...` in the [config](#config).

### Optional: `fzf`

[`fzf`](https://github.com/junegunn/fzf) is the fuzzy-finder used by the no-arg picker mode — `mdp` with no file argument fzf-picks a `.md` from the current directory. If `fzf` is missing, the help text is printed instead.

## Usage

```sh
mdp README.md                  # render + open in browser
mdp                            # fzf-pick a .md from cwd, then preview
mdp -e README.md               # preview AND open the file in nvim
mdp -e                         # fzf-pick, preview, and edit
mdp -t light README.md         # light theme
mdp -p README.md               # print HTML path, don't open browser
mdp serve README.md 8080 dark  # plugin server mode (used by md-preview.nvim)
mdp help                       # show help
```

### Keys

| Action            | Default   | Colemak   |
| ----------------- | --------- | --------- |
| Down / Up         | `j` / `k` | `n` / `e` |
| Left / Right      | `h` / `l` | `h` / `i` |
| Half-page down/up | `d` / `u` | `d` / `u` |
| Top / Bottom      | `g` / `G` | `g` / `G` |
| Close             | `q`       | `q`       |

Enable Colemak with `colemak = true` in the [config](#config).

### Notes

The preview is static — re-run `mdp` to refresh, or install the [Neovim plugin](#neovim-plugin) for live scroll-sync.

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

## Contributing

Issues and PRs welcome at [github.com/aldevv/md-preview/issues](https://github.com/aldevv/md-preview/issues).

```sh
make test     # go test ./...
make build    # produces ./mdp
make install  # go install ./cmd/mdp
```

## License

MIT — see [LICENSE](LICENSE).

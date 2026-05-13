# LaTeX support

mdp renders LaTeX in two places:

1. **Whole `.tex` files**: `mdp foo.tex` / `mdp watch foo.tex` / `mdp serve foo.tex …`.
2. **Fenced ` ```latex ` blocks inside markdown**: the block body is rendered as LaTeX, the surrounding `.md` continues to render normally.

Both paths shell out to a host `pandoc` binary:

```
pandoc --from=latex --to=html5 --mathjax --no-highlight --sandbox
```

## Requirements

`pandoc` must be on `$PATH`:

```sh
apt install pandoc   # Debian/Ubuntu
brew install pandoc  # macOS
pacman -S pandoc     # Arch
```

`mdp foo.tex` without pandoc prints the install hint and exits non-zero. Math-free `.md` files have no pandoc dependency.

## Why pandoc subprocess (and not bundled WASM)

mdp originally embedded Pandoc's wasm32-wasi build to render LaTeX entirely in the browser. That worked but cost ~58 MB of binary size and ~2 s of WASM compile per launch. Host pandoc renders the same input in ~13 ms.

The WASM path also required a custom http server in the child process and ran into a series of WebKitGTK quirks (MIME propagation, IDB structured-clone of `WebAssembly.Module`, wasm code cache scope) that meant warm runs never got much faster than cold. Dropping the bundle in favor of the host binary made the code smaller, the binary smaller, and the runtime faster, at the cost of a one-line install for users who don't already have pandoc.

The full WASM detour lives in the git history (`latex-wasm-native-window` branch) if anyone wants to study the WebKitGTK + pandoc-wasm integration.

## Architecture

```
cmd/mdp/main.go
  + RenderBody returns latex.ErrPandocNotFound for .tex inputs when
    pandoc isn't installed; main.go prints an install hint and exits 1.

internal/render/latex/latex.go
  PandocAvailable() bool
  Render(ctx, src []byte, sourceDir string) (string, error)
  ErrPandocNotFound, ErrOutputTooLarge

internal/render/render.go
  + emitLatexFence calls latex.Render and emits <div class="latex-block">
    on success or <div class="latex-error"> on failure (visible in the
    preview rather than silently dropped).
  + RenderBody on .tex/.latex returns the rendered HTML or surfaces
    ErrPandocNotFound to the caller.
```

## Pandoc invocation

- Command: `pandoc --from=latex --to=html5 --mathjax --no-highlight --sandbox`.
- `--mathjax` makes pandoc emit math as `\(…\)` / `\[…\]` markers; KaTeX (bundled in the page) picks them up at render time.
- `--no-highlight` avoids pandoc's syntax highlighter clashing with the bundled highlight.js.
- `--sandbox` blocks pandoc from reading any file IO during the render. Trade: multi-file LaTeX (`\input{}`, `\includegraphics{}` from disk) doesn't resolve, in exchange for protection against malicious `.tex` exfiltrating arbitrary user-readable files via the loopback origin.
- Source goes via stdin, no shell quoting risk.
- Output is post-sanitized via `bluemonday.UGCPolicy()` before injection. Strips `javascript:` URLs, `<script>` tags, and other XSS vectors that pandoc happily passes through from a malicious `\href{}` or raw HTML.
- 5 s context timeout per render, 20 MiB output cap.

## Embedded fenced LaTeX in markdown

````
Prose paragraph.

```latex
\section{Hello}
This is rendered.
```

More prose.
````

Output: the fence is replaced by pandoc-rendered HTML wrapped in `<div class="latex-block" data-line="N">`. `data-line` keeps the goldmark scroll-sync attribution intact at block granularity; scroll-sync inside the rendered LaTeX content is not preserved (block-level only).

## Scroll-sync

Block-level for `.tex` files and fenced blocks: the WS scroll handler in `page.go` walks `data-line` elements. Line-level scroll-sync inside a rendered LaTeX block is deferred.

## Security & resource hygiene

- pandoc runs as a subprocess. No user-supplied flags. Source is delivered via stdin.
- pandoc's `--sandbox` prevents file IO + network fetches during render.
- Output is bluemonday-sanitized server-side.
- Output cap (20 MiB) and time cap (5 s) bound runaway compiles.
- HTML output is trusted enough after sanitization to inject into the loopback origin.

## Non-goals

- **TikZ / PSTricks / PGFPlots.** Pandoc can't render these without a real TeX engine.
- **`\includegraphics{foo.pdf}` / EPS figures.** Browsers handle PDFs inconsistently and won't show EPS.
- **`\input{}` cross-file LaTeX.** `--sandbox` blocks it.
- **Bibliography.** Citations render as `[?]` without a `.bib` file passed via `--bibliography`.
- **Editor scroll-sync inside `.tex` content.** Block-level only.

## Tests

- `internal/render/render_test.go`:
  - `TestRenderFencedLatex_PandocRenders` — fenced `` ```latex `` produces `.latex-block` with pandoc-rendered HTML.
  - `TestRenderFencedLatex_PandocMissingShowsError` — fenced block with pandoc missing emits `.latex-error` containing the install hint.
  - `TestRenderBody_TexExtensionRequiresPandoc` — `.tex` without pandoc returns `ErrPandocNotFound`.
  - `TestRenderFencedLatex_DataLinePreserved` — fenced block carries the source line for scroll-sync.

- Fixtures:
  - `testdata/sample.tex` — sections, lists, math, a tabular.
  - `testdata/embedded_latex.md` — one fenced LaTeX block in prose.

# LaTeX support

mdp can render LaTeX in two places:

1. **Whole `.tex` files**: `mdp foo.tex` / `mdp watch foo.tex` / `mdp serve foo.tex …`.
2. **Fenced ` ```latex ` blocks inside markdown**: the block body is rendered as LaTeX, the surrounding `.md` continues to render normally.

Both paths go through the same backend: a subprocess to `pandoc --from=latex --to=html5 --mathjax --no-highlight --sandbox`.

## Why pandoc

Pandoc covers ~all of the common LaTeX features (sectioning, lists, refs, citations, tables, common math) with a one-time `apt install pandoc` / `brew install pandoc`. Alternatives considered and rejected:

- **Bundled `latex.js`**: pure-JS LaTeX → HTML compiler, ~3.5 MB. No runtime dep but materially smaller LaTeX coverage and inflates the binary substantially.
- **Shelling out to `tex4ht` / `make4ht`**: highest fidelity, but requires a full TeX Live install on the user's machine (~6 GB).

`docs/latex.md` (this file) captures the design.

## Architecture

```
cmd/mdp/main.go
  + runs the renderer chosen by file extension:
      .md / .markdown  -> internal/render (goldmark)
      .tex             -> internal/render/latex (pandoc subprocess)

internal/render/latex/latex.go
  Render(ctx, src []byte, sourceDir string) (htmlBody string, err error)

internal/render/render.go
  + intercepts FencedCodeBlock when info string is "latex" / "tex" /
    "pandoc-latex", routing the body through internal/render/latex.

internal/render/page.go
  + inlines KaTeX CSS + JS so client-side math rendering picks up
    pandoc's `\(...\)` and `\[...\]` output, plus inline $..$ in
    plain markdown.

internal/server/server.go
  + no changes; watch + WS reload work as-is.
```

## Pandoc invocation

- Command: `pandoc --from=latex --to=html5 --mathjax --no-highlight --sandbox`. `--mathjax` instructs pandoc to emit math as `\(...\)` / `\[...\]` markers (the delimiters KaTeX's auto-render handles); KaTeX itself is bundled into the page. `--no-highlight` avoids pandoc's syntax highlighter clashing with mdp's bundled highlight.js for any non-math code. `--sandbox` blocks pandoc from reading any file IO during the render: this trades multi-file LaTeX support (`\input{}`, `\includegraphics{}` from disk) for protection against malicious `.tex` files exfiltrating `/etc/passwd` or anything else the user can read.
- Source goes via **stdin**; no shell quoting risk.
- Pandoc HTML output is **post-sanitized via bluemonday's `UGCPolicy`** before injection into the loopback origin. Strips `javascript:` URL schemes, `<script>` tags, and other XSS vectors that pandoc happily passes through from a malicious `\href{}` or raw HTML pass-through.
- 5s context timeout per render to bound runaway compiles.
- 20 MiB output cap; surfaced via `ErrOutputTooLarge`. `limitWriter` drains in cap-overflow mode so pandoc finishes promptly instead of blocking on a full pipe until the timeout.
- `ErrPandocNotFound` sentinel when `exec.LookPath("pandoc")` fails. The caller in `RenderBody` substitutes a `<p>` body with "install pandoc via apt install pandoc or brew install pandoc" instead of leaking the raw error string.
- `Options.Warnings io.Writer` (optional) receives pandoc's stderr on successful renders. Pandoc emits diagnostics (missing macros, sandbox rejections) even on exit 0; this lets callers surface them without blocking on the stderr buffer.

## Embedded fenced LaTeX in markdown

Input:

````
Prose paragraph.

```latex
\section{Hello}
This is rendered.
```

More prose.
````

Output: the fence is replaced by pandoc-rendered HTML wrapped in a `<div class="latex-block" data-line="N">`. `data-line` keeps the goldmark scroll-sync attribution intact at block granularity, even though scroll-sync inside the rendered LaTeX is not preserved.

## Scroll-sync

**Deferred to v2.** `.tex` previews scroll independently of the editor; the markdown-fenced-LaTeX case keeps block-level `data-line` only. Plugin-side WS scroll messages targeted at lines inside a `.tex` document are silently no-ops.

A v2 implementation could either (a) emit `data-line` annotations from a custom LaTeX parser, or (b) ask pandoc for source-position info via `--track-changes` or similar. Neither is on the v1 critical path.

## Security & resource hygiene

- pandoc runs as a subprocess. We never pass user-supplied flags. Source is delivered via stdin.
- Resource confinement: pandoc only reads `\input{}` / `\includegraphics{}` paths relative to `sourceDir` (matching the existing `pathInsideDir` discipline in `internal/server/server.go`).
- Output cap and time cap as noted above.
- HTML output from pandoc is trusted (pandoc emits well-formed HTML). The user's `.tex` is the untrusted source; the only way pandoc-rendered HTML contains JS is if the user wrote `\href{javascript:...}{}` or similar, which pandoc strips by default.

## CLI / config

- No new CLI flag.
- No config knobs in v1. The renderer always engages when pandoc is on PATH and the file extension is `.tex` / `.latex` (or a fence info string matches). If pandoc is missing, the install hint surfaces in the preview body and other rendering keeps working.

Future-work knobs that would fit `config.toml` once there's demand:

```toml
latex = true              # default; false would disable both .tex dispatch and fenced-latex blocks
pandoc_path = "pandoc"    # would override the PATH lookup
```

Adding either requires plumbing through `internal/config/config.go` and the renderer dispatch in `internal/render/render.go`; deliberately deferred.

## Non-goals (v1)

- **TikZ / PSTricks / PGFPlots.** Pandoc can't render these without a real TeX engine. Documents using them render with the drawing commands dropped.
- **`\includegraphics{foo.pdf}` / EPS figures.** Browsers handle PDFs inconsistently and won't show EPS at all. v1 leaves the `<img src=...>` and lets the browser do what it does.
- **Bibliography.** Citations render as `[?]` without a `.bib` file passed via `--bibliography`. v2 could auto-detect a sibling `.bib` and add the flag.
- **Editor scroll-sync for `.tex`.** See above.

## Tests

- `internal/render/latex/latex_test.go`:
  - Happy path round-trip on a minimal `.tex`.
  - Malformed input → captured stderr surfaces as an error.
  - Missing pandoc → `ErrPandocNotFound`. Caller-side: `RenderBody` substitutes a friendly install hint.
  - `\input{subdir/file.tex}` resolves under `sourceDir`.
  - Tests `t.Skip` if pandoc isn't on PATH (CI installs it explicitly).
- `internal/render/render_test.go`:
  - Fenced ```latex round-trips through pandoc.
  - Non-matching fence (```go) keeps highlight.js behavior.
- Fixtures:
  - `testdata/sample.tex`: sections, lists, math, a `\cite{}`, a tabular.
  - `testdata/embedded_latex.md`: one fenced LaTeX block in prose.

## Estimated effort

~1.5 days end to end:

| Phase | Time |
| --- | --- |
| `internal/render/latex` package + pandoc subprocess | 3h |
| Renderer dispatch in `cmd/mdp/main.go` | 1h |
| Fenced ```latex intercept in markdown renderer | 2h |
| KaTeX bundling + page template hooks | 2h |
| Tests + fixtures | 3h |
| Manual smoke (`.tex`, embedded fence, math, missing pandoc) | 1h |

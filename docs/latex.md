# LaTeX support

mdp renders LaTeX in two places:

1. **Whole `.tex` files**: `mdp foo.tex` / `mdp watch foo.tex` / `mdp serve foo.tex …`.
2. **Fenced ` ```latex ` blocks inside markdown**: the block body is rendered as LaTeX, the surrounding `.md` continues to render normally.

Both paths funnel the LaTeX source into the **browser**, where `pandoc.wasm` (Pandoc 3.9's official wasm32-wasi build) converts it to HTML. mdp ships the WASM, its JS bridge, the WASI shim, and DOMPurify as embedded static assets; no external `pandoc` binary on PATH.

## Why client-side WASM

Earlier iterations of this package shelled out to `pandoc` on the host. That worked but forced every user to `apt install pandoc` / `brew install pandoc` separately. Two alternative server-side approaches were considered and rejected before settling on client-side WASM:

- **wazero (pure-Go WASM runtime)**: blocks on the WebAssembly exception-handling proposal that GHC's WASM backend emits. wazero issue [#2426](https://github.com/wazero/wazero/issues/2426) tracks the gap; no implementation in sight.
- **`wasmtime-go` (cgo bindings)**: works, but converts mdp from a pure-Go static binary into a cgo binary with platform-specific build/distribution pain. The single-binary distribution story is one of mdp's core properties.

Client-side WASM via [`@bjorn3/browser_wasi_shim`](https://github.com/bjorn3/browser_wasi_shim) keeps mdp pure Go, leverages the official MIT-licensed `pandoc.js` interface module, and runs the WASM in the browser's mature JS engine.

## Architecture

```
cmd/mdp/main.go
  + Auto-promotes static-mode (.tex / .md with latex fences) to the
    HTTP server path: file:// URLs cannot fetch WASM bundles, so a
    static .tex preview boots the same server `mdp watch` uses.

internal/render/latex/latex.go
  Placeholder(src []byte, dataLine string) string
  HasLatex(body string) bool
  AssetsFS() fs.FS  // pandoc.wasm + JS bundle, served under /_/

internal/render/render.go
  + Both whole-.tex and fenced ```latex go through latex.Placeholder
    and emit <div class="latex-pending" data-src="…base64…"> nodes
    that the page-side renderer swaps with pandoc.wasm's HTML.

internal/render/page.go
  + Inlines KaTeX CSS + JS when any math marker or latex-pending
    placeholder is in the body. When latex-pending is present, also
    wires up DOMPurify + the latex-render.js ES module.

internal/server/server.go
  + /_/<asset> route serves the embedded WASM/JS bundle. Explicit
    Content-Type (application/wasm) so WebAssembly.instantiateStreaming
    accepts it.
```

## Browser-side flow

1. Server renders the body. Fences/`.tex` files become `<div class="latex-pending" data-src="<base64>">Rendering LaTeX…</div>`.
2. Page loads with DOMPurify (sync) and `latex-render.js` (module).
3. `latex-render.js` imports `pandoc.js`, which fetches `pandoc.wasm` + the WASI shim and runs the Haskell-init dance once.
4. The module walks every `.latex-pending`, decodes its `data-src` base64, calls `convert({from:"latex", to:"html5", mathjax:true, "no-highlight":true, sandbox:true}, source, {})`, sanitizes the result with DOMPurify, and replaces the placeholder with `<div class="latex-block" data-line="N">…</div>`.
5. `mdpRenderMath()` re-runs over the new HTML so KaTeX picks up the `\(…\)` / `\[…\]` markers Pandoc emits in `--mathjax` mode.
6. WS reload (live preview) re-runs `mdpRenderLatex(document)` after swapping `#content`, so newly-introduced fences render too.

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

Server emits:

```html
<p data-line="1">Prose paragraph.</p>
<div class="latex-pending" data-line="3" data-src="…base64…">Rendering LaTeX…</div>
<p data-line="7">More prose.</p>
```

After the browser renders, the placeholder div is replaced with a `<div class="latex-block" data-line="3">` containing pandoc's HTML.

## Assets bundled

| File | Source | License | Size |
|---|---|---|---|
| `pandoc.wasm` | [Pandoc 3.9.0.2 release](https://github.com/jgm/pandoc/releases/tag/3.9.0.2) | GPL-2.0+ | 58 MB (15 MB gzipped) |
| `pandoc.js` | [`jgm/pandoc/wasm/pandoc.js`](https://github.com/jgm/pandoc/blob/main/wasm/pandoc.js) (CDN import rewritten to a local path) | MIT | 6 KB |
| `wasi-shim.js` | [`@bjorn3/browser_wasi_shim@0.3.0`](https://github.com/bjorn3/browser_wasi_shim) (jsdelivr ESM bundle) | Apache-2.0 / MIT | 26 KB |
| `purify.min.js` | [DOMPurify 3.2.4](https://github.com/cure53/DOMPurify) | MPL-2.0 or Apache-2.0 | 22 KB |
| `latex-render.js` | mdp glue (this repo) | Apache-2.0 | <2 KB |

The total contribution to the mdp binary is ~60 MB; everything is served once per browser session and cached.

## License note

`pandoc.wasm` is GPL-2.0-or-later. It is shipped as a static asset served over loopback and is never linked into any Go code path — the binary embeds it as a byte slice and writes it to the network on request. This matches how a Go web server distributing a GPL-licensed downloadable file would normally be structured ("mere aggregation"). Anyone redistributing the mdp binary still needs to honor the GPL: include a copy of the upstream license at `internal/render/latex/wasm/COPYING` and remain prepared to provide the corresponding source on request (the source lives at github.com/jgm/pandoc).

## Sandboxing

`pandoc.wasm` is fundamentally sandboxed: it runs in the browser's WASM sandbox with a preopened in-memory filesystem (just the four virtual files `stdin`/`stdout`/`stderr`/`warnings`), no network, no host file access. The CLI's `--sandbox` flag is still passed for defense-in-depth — it tells the Pandoc reader to refuse unsafe raw commands at the Haskell level, regardless of what the underlying environment allows.

Pandoc's HTML output is then **DOMPurify-sanitized in the browser** before `innerHTML` injection. Pandoc's own URL filter already strips `javascript:` schemes, but `\begin{html}…` or raw HTML pass-through could otherwise smuggle `<img onerror>` or similar past it. DOMPurify defaults (`USE_PROFILES: {html: true}`) preserve KaTeX's `\(…\)` math markers as text while stripping every dangerous element/attribute.

## Static mode auto-promotion

`mdp foo.tex` and `mdp foo.md` (with `\`\`\`latex` fences) cannot run as `file://` previews because the browser refuses to fetch WASM bundles from a `file://` origin. When `latex.HasLatex(body)` returns true, mdp logs `mdp: LaTeX detected; starting preview server (Ctrl-C to exit)` and runs the same auto-reload HTTP server `mdp watch` uses. Math-free `.md` files still use the original write-tmp-and-open-file:// path.

The one regression: `mdp -p foo.tex` (print HTML path instead of opening a browser) returns an error. The `-p` flow assumes a static HTML file the user can pipe elsewhere; client-side WASM rendering needs a live server, so there's no single self-contained HTML to print. Use `mdp watch foo.tex` and grab the URL from `[md-preview] Serving on …` instead.

## Scroll-sync

**Deferred to v2.** `.tex` previews scroll independently of the editor; the markdown-fenced-LaTeX case keeps block-level `data-line` only (stamped on the placeholder and preserved on the rendered wrapper). Plugin-side WS scroll messages targeted at lines inside a `.tex` document are silently no-ops.

## CLI / config

- No new CLI flag.
- No config knobs. The renderer always engages when the file extension is `.tex` / `.latex` or a fence info string matches.
- `mdp foo.tex` switches transparently to server-mode (see "Static mode auto-promotion" above).

Future-work knob:

```toml
latex = true   # default; false would disable .tex dispatch + fenced-latex
```

Adding it requires plumbing through `internal/config/config.go` and the renderer dispatch; deliberately deferred.

## Non-goals (v1)

- **TikZ / PSTricks / PGFPlots.** Pandoc can't render these without a real TeX engine. Documents using them render with the drawing commands dropped.
- **`\includegraphics{foo.pdf}` / EPS figures.** Browsers handle PDFs inconsistently and won't show EPS at all. v1 leaves the `<img src=…>` and lets the browser do what it does.
- **`\input{}` cross-file LaTeX.** The WASM filesystem is in-memory; there's no way to feed it sibling files from disk. v2 could pre-walk the source and stuff resolved files into the `files` map argument of `convert()`.
- **Bibliography.** Citations render as `[?]` without a `.bib` file passed via `--bibliography`. v2 could auto-detect a sibling `.bib`.
- **Editor scroll-sync for `.tex`.** See above.
- **`mdp -p foo.tex` static HTML.** See "Static mode auto-promotion."

## Tests

- `internal/render/render_test.go`:
  - `TestRenderFencedLatex_EmitsPlaceholder` — fenced `\`\`\`latex` yields a `latex-pending` div with base64 source.
  - `TestRenderFencedTex_AlsoPlaceholder` — `\`\`\`tex` info string treated the same.
  - `TestRenderBody_TexExtensionEmitsPlaceholder` — `.tex` extension routes through the placeholder, not goldmark.
  - `TestRenderFencedLatex_DataLinePreserved` — `data-line` on the placeholder matches the fence's source line.
- `internal/server/server_test.go`:
  - `TestHandler_LatexAsset_PandocWasm` — `/_/pandoc.wasm` returns `application/wasm` and starts with the WASM magic.
  - `TestHandler_LatexAsset_JSandCSS` — `/_/pandoc.js`, `/_/wasi-shim.js`, `/_/latex-render.js`, `/_/purify.min.js` all return correct MIME + sentinel content.
  - `TestHandler_LatexAsset_RejectsTraversal` — `../` and trailing-slash variants get rejected.
  - `TestHandler_LatexPage_WiresUpScripts` — a `.md` with a latex fence emits the placeholder div AND the script tags.
  - `TestHandler_NoLatex_NoWasmScripts` — math-free `.md` skips the bundle.

No `latex_test.go` anymore — the old pandoc-subprocess tests are gone, and the new `latex.go` only exposes `Placeholder` (well-covered transitively) and `AssetsFS` (covered by the server tests).

- Fixtures:
  - `testdata/sample.tex`: sections, lists, math, a `\cite{}`, a tabular.
  - `testdata/embedded_latex.md`: one fenced LaTeX block in prose.

## Manual smoke test

The Go tests cover the wiring; verify the end-to-end browser flow with:

```
mdp watch testdata/sample.tex
```

You should see:

1. Preview opens with a dashed-border "Rendering LaTeX…" placeholder for a beat.
2. Placeholder is replaced with rendered LaTeX (`<h1>`, `<em>`, lists, table).
3. Inline `$E = mc^2$` and display `$$\sum$$` math typeset by KaTeX (after pandoc emits the markers).
4. Browser devtools network tab shows one GET each for `/_/pandoc.wasm`, `/_/pandoc.js`, `/_/wasi-shim.js`, `/_/latex-render.js`, `/_/purify.min.js`.

For the embedded-fence case:

```
mdp watch testdata/embedded_latex.md
```

The surrounding markdown renders immediately; the fenced LaTeX block(s) fill in after WASM init.

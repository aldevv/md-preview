# PDF export

`mdp pdf <file> [-o output.pdf]` renders a markdown file to PDF.

## Why headless Chrome (and not pandoc, wkhtmltopdf, weasyprint, a pure-Go lib)

The existing `mdp` pipeline already renders markdown to HTML using
goldmark, with theme CSS + highlight.js + (if we add it) KaTeX inlined.
Whatever renders that HTML to PDF needs to honor the same CSS we already
serve in the live preview, so the printed page looks like the on-screen
page. Realistic options:

- **Headless Chrome** (`chromium --headless --print-to-pdf`): uses the
  same Blink engine that's already opening the live preview for the
  majority of mdp users. CSS and JS support are total. One subprocess
  invocation. Zero new Go deps.
- **wkhtmltopdf**: archived, ships old WebKit, fails on modern CSS.
- **WeasyPrint**: Python dep on the user's machine; defeats the
  single-binary distribution story.
- **Pandoc**: needs a LaTeX engine for PDF (`pdflatex` etc.), or another
  backend, and doesn't honor our HTML's CSS the way Chrome does.
- **`gofpdf` / pure-Go HTML→PDF**: nothing mature exists at the quality
  needed; would mean translating CSS to PDF drawing primitives by hand.

Headless Chrome wins on quality and integration. Users who already use
mdp likely already have Chromium installed for the existing `--app=URL`
preview path, so the dependency surface doesn't grow.

## Architecture

```
cmd/mdp/main.go
  + case "pdf": runPDF(args, stdout, stderr, env)

cmd/mdp/pdf.go (new)
  Parses flags. Renders the .md to HTML via the existing
  render.RenderBody + render.BuildPage. Writes the HTML to a temp file.
  Resolves a Chromium binary via internal/config.ChromiumPath. Spawns
  chrome --headless --disable-gpu --print-to-pdf=<out> file://<tmp>
  synchronously and waits.

internal/render/pdf/pdf.go (new)
  Render(htmlPath, outPath, chromiumBin string,
         run func(string, []string, []string) error) error
  Builds the chrome argv. Shells out via env.RunCmd. Returns a wrapped
  error on failure with a hint to install Chrome/Chromium if the binary
  was not found upstream.

internal/config/config.go
  + ChromiumPath(lookPath, goos) string
  Extracted from the existing autoBrowserFamilies[0] / autoMacAppBundles
  data so the same Chromium-family detection serves both the --app=URL
  spawn and the PDF backend. Empty string when no Chromium is found.
```

The HTTP server is NOT involved: PDF generation is a one-shot
batch command, not a live-preview interaction. The temp HTML is read
via `file://` so the loopback HTTP server doesn't need to be running.

## CLI

```
mdp pdf <file> [-o output.pdf] [--theme dark|light]
```

Defaults:
- `output.pdf` defaults to the same directory as the input file with
  the basename + `.pdf` extension. `foo.md` → `foo.pdf`.
- `--theme` defaults to the config file's theme, falling back to dark.
  Same precedence as `mdp <file>`.

Exit codes:
- 0: PDF written.
- 1: file not found, render error, or Chrome failure.

## Phase 2 (deferred): native WebView `printToPDF`

When the native-window code path matures (currently gated behind
`MDP_NATIVE=1`), the in-process WebView can export PDF without
shelling out to Chrome:

- **macOS** (`WKWebView.createPDF`): the Cocoa shim in
  `internal/nativewin/nativewin_darwin.go` already drives WKWebView via
  `purego/objc`. Adding a Cmd-P keybinding that calls `createPDF` and
  saves the resulting `NSData` to a file is ~80 LOC.
- **Linux** (`webkit_print_operation_print_to_pdf` on WebKitGTK):
  similar shape via the existing purego dlopen of libwebkit2gtk.

Native printToPDF wins on: no Chrome dep at all, no subprocess
overhead, save-to-file dialog feels native. Loses on: only works for
users who opted into the native window, and per-platform FFI to write
and test. Deferred until native-window flips on by default.

## Error paths

- **Chromium not found**: print "PDF export needs Chrome/Chromium.
  Install via `apt install chromium`, `brew install --cask chromium`,
  or visit https://www.google.com/chrome." Exit 1.
- **Chrome exits non-zero**: surface stderr; exit 1.
- **Input file missing**: usual `file not found` error.
- **`-o` path is in a non-writable directory**: surface the write error;
  exit 1.

## Resource hygiene

- Chrome subprocess is bounded by a 60s context timeout. PDF rendering
  for big docs can take a few seconds but minutes is pathological.
- Temp HTML is written via the existing `writeTmpFile` (O_NOFOLLOW,
  0600 mode), reusing the `tmpHTMLPath` sha-stable naming from the
  static-preview path.
- The output PDF is written by Chrome directly; we don't buffer it in
  memory.

## Tests

- `internal/render/pdf/pdf_test.go`:
  - Happy path: stub `run` succeeds, returns nil.
  - Chrome failure: stub `run` returns a non-nil error; Render wraps it.
  - argv shape: capture the args passed to `run` and assert the expected
    `--headless --disable-gpu --print-to-pdf=...` flags appear.
- `cmd/mdp/pdf_test.go`:
  - End-to-end with a stubbed Environment.RunCmd (no actual chrome
    needed): file not found, default output path computation, `-o`
    override, missing chromium surfaces install hint.
- Real Chrome smoke test deferred to manual: `mdp pdf testdata/sample.md`
  on the dev box.

## Non-goals (v1)

- Multi-page table of contents / headers + footers / page numbers.
  Chrome's print CSS handles basic page breaks but we don't customize.
- Interactive PDF features (form fields, JS execution post-render).
- Watermarks / branding.
- PDF/A or signed PDFs.

## Effort estimate

| Phase | Work | Time |
| --- | --- | --- |
| 1 | `internal/render/pdf` + `cmd/mdp/pdf.go` + ChromiumPath helper + tests + docs | ~4h |
| 2 | Native WKWebView createPDF + WebKitGTK print-to-pdf + JS Cmd-P bridge | ~6h per platform |

Phase 1 is what this branch ships. Phase 2 lives in `## Phase 2`
above and is gated on the native-window code path being the default.

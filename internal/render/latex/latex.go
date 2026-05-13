// Package latex emits client-side WASM placeholders for LaTeX content
// in mdp's preview page and exposes the embedded pandoc-wasm bundle.
//
// The previous version of this package shelled out to a host-installed
// pandoc binary. That worked but required users to install pandoc
// separately. The current version ships pandoc's official wasm32-wasi
// build inside the mdp binary; the browser executes it via @bjorn3's
// WASI shim and DOMPurify-sanitizes the HTML before injection.
package latex

import (
	"embed"
	"encoding/base64"
	"fmt"
	gohtml "html"
	"io/fs"
	"strings"
)

// AssetsFS holds pandoc.wasm.gz + the JS bridge + WASI shim + DOMPurify
// + mdp's glue script. Sourced from internal/render/latex/wasm/.
//
// pandoc.wasm.gz is the gzip -9'd Tweag/Pandoc WASM build (GPL-2.0+);
// it's shipped as a static asset served with Content-Encoding: gzip
// so the browser decodes on the fly via WebAssembly.instantiateStreaming.
// Storing gzipped (~16 MB) instead of raw (~58 MB) saves ~42 MB in the
// mdp binary at zero runtime cost (browsers transparently decode gzip
// in streaming WASM instantiation).
//
// pandoc.js (MIT, Tweag) is the upstream interface module with one
// local-import rewrite. wasi-shim.js is the jsdelivr ESM bundle of
// @bjorn3/browser_wasi_shim (Apache-2.0/MIT). purify.min.js is
// DOMPurify (Apache-2.0/MPL). latex-render.js is mdp's own glue.
//
//go:embed wasm
var assets embed.FS

// AssetsFS returns the embedded asset tree rooted at "wasm/" so the
// server can serve every file via http.StripPrefix("/_/").
func AssetsFS() fs.FS {
	sub, err := fs.Sub(assets, "wasm")
	if err != nil {
		// fs.Sub only errors on a malformed path, which is a compile-time
		// constant here; panic so a build issue fails loudly rather than
		// silently dropping LaTeX support at runtime.
		panic(fmt.Errorf("latex: embed sub: %w", err))
	}
	return sub
}

// Placeholder wraps a LaTeX source fragment in a <div class="latex-pending">
// the browser-side latex-render.js will swap with pandoc.wasm's HTML output.
//
// The source is base64-encoded so arbitrary content (newlines, quotes,
// braces, raw HTML inside \begin{verbatim}) survives a single DOM
// attribute round-trip without needing per-character escaping.
//
// dataLine is the 1-indexed source line of the fence/document opening;
// it stamps the placeholder for scroll-sync, and is preserved on the
// rendered wrapper. Pass an empty string for whole-.tex documents
// where line-level scroll-sync is deferred to v2.
func Placeholder(src []byte, dataLine string) string {
	b64 := base64.StdEncoding.EncodeToString(src)
	var b strings.Builder
	b.WriteString(`<div class="latex-pending"`)
	if dataLine != "" {
		b.WriteString(` data-line="`)
		b.WriteString(gohtml.EscapeString(dataLine))
		b.WriteString(`"`)
	}
	b.WriteString(` data-src="`)
	b.WriteString(b64)
	b.WriteString(`">Rendering LaTeX…</div>`)
	return b.String()
}

// HasLatex reports whether body contains any latex-pending placeholder
// the client-side renderer must process. BuildPage uses this to skip
// the ~58 MB pandoc.wasm bundle for the math-free common case.
func HasLatex(body string) bool {
	return strings.Contains(body, `class="latex-pending"`)
}

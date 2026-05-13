// Package latex turns LaTeX source into HTML via two paths:
//
//   - Host pandoc subprocess (pandoc.go): used when `pandoc` is on
//     PATH. ~13 ms warm; the fast path for `mdp foo.tex`.
//   - Browser WASM via pandoc-wasm (wasm.go): used when pandoc isn't
//     installed. ~2 s, runs in the preview's WebView.
//
// render.go and cmd/mdp call PandocAvailable() and dispatch to either
// Render() (subprocess) or Placeholder() (WASM placeholder for the
// browser-side renderer to resolve).
package latex

// Version pins the pandoc.wasm artifact this build embeds. Used to
// version the WASM-mode sibling cache dir so multiple mdp installs
// coexist and stale blobs are obvious. Bump in lockstep with the
// Makefile's PANDOC_WASM_VERSION.
const Version = "3.9.0.2"

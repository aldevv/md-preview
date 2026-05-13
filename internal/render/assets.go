package render

import _ "embed"

// highlight.js + its github / github-dark themes are embedded at build time
// (sourced from cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/) and
// inlined into the HTML by BuildPage. Inlining avoids a render-blocking
// CDN round-trip on every preview, which is the dominant cause of
// noticeable "browser takes a while to paint" hangs on cold caches.

//go:embed assets/highlight.min.js
var hljsScript string

//go:embed assets/github-dark.min.css
var hljsThemeDarkCSS string

//go:embed assets/github.min.css
var hljsThemeLightCSS string

// KaTeX 0.16.11 (https://github.com/KaTeX/KaTeX/releases/tag/v0.16.11),
// inlined for the same reason as highlight.js: zero network on render.
// The CSS has every @font-face's woff2 baked in as a base64 data URI so
// fonts work in both `mdp serve` (HTTP) and `mdp <file>` (static
// file:// URL) modes. ~367 KiB CSS + ~275 KiB JS + ~3 KiB auto-render.

//go:embed assets/katex.min.css
var katexCSS string

//go:embed assets/katex.min.js
var katexScript string

//go:embed assets/katex-auto-render.min.js
var katexAutoRenderScript string

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

// mermaid 11.x (https://github.com/mermaid-js/mermaid), inlined for
// the same reason as highlight.js / KaTeX: zero network on render.
// ~3.3 MiB. Only loaded into the page when a ```mermaid fence is
// present (hasMermaid in page.go gates this).

//go:embed assets/mermaid.min.js
var mermaidScript string

// Page-template CSS lives in assets/css/ as actual .css files so an
// editor can syntax-highlight, lint, and format it. The Go code just
// holds the embedded strings.

//go:embed assets/css/theme-dark.css
var CSSDark string

//go:embed assets/css/theme-light.css
var CSSLight string

//go:embed assets/css/markdown.css
var CSSCommon string

//go:embed assets/css/pandoc.css
var pandocCSS string

//go:embed assets/css/chrome.css
var chromeCSS string

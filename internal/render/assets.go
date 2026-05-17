package render

import _ "embed"

// Third-party JS and CSS are embedded at build time and inlined by
// BuildPage. A render-blocking CDN fetch on every preview is the
// dominant cause of cold-cache paint stalls; inlining keeps the page
// self-contained for both `mdp serve` (HTTP) and `mdp <file>`
// (file://) modes. KaTeX's CSS bakes its woff2 fonts in as base64 so
// the file:// path renders math without missing-glyph boxes. mermaid
// only loads when a ```mermaid fence is present (gated by hasMermaid
// in page.go).

//go:embed assets/highlight.min.js
var hljsScript string

//go:embed assets/github-dark.min.css
var hljsThemeDarkCSS string

//go:embed assets/github.min.css
var hljsThemeLightCSS string

//go:embed assets/katex.min.css
var katexCSS string

//go:embed assets/katex.min.js
var katexScript string

//go:embed assets/katex-auto-render.min.js
var katexAutoRenderScript string

//go:embed assets/mermaid.min.js
var mermaidScript string

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

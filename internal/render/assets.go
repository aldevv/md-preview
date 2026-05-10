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

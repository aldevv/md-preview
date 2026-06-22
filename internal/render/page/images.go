package page

import (
	"regexp"
	"strings"
)

var imgTagRE = regexp.MustCompile(`<img\b[^>]*>`)

// MarkImagesAsync adds loading="lazy" decoding="async" to every <img>
// that doesn't already carry those attributes. WebKit otherwise blocks
// first-contentful-paint on external image fetches (e.g. shields.io
// badges), which on cold start makes the window look black for the
// duration of the slowest image fetch.
func MarkImagesAsync(html string) string {
	return imgTagRE.ReplaceAllStringFunc(html, func(match string) string {
		out := match
		if !strings.Contains(out, " loading=") {
			out = strings.Replace(out, "<img", `<img loading="lazy"`, 1)
		}
		if !strings.Contains(out, " decoding=") {
			out = strings.Replace(out, "<img", `<img decoding="async"`, 1)
		}
		return out
	})
}

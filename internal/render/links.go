package render

import (
	gohtml "html"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var imgSrcRE = regexp.MustCompile(`(<img\b[^>]*\bsrc=")([^"]+)(")`)
var imgTagRE = regexp.MustCompile(`<img\b[^>]*>`)

// build returning ok=false preserves the original src so callers can
// refuse out-of-tree paths without breaking sibling imgs.
func RewriteImgSrc(html, baseDir string, build func(absPath string) (string, bool)) string {
	return imgSrcRE.ReplaceAllStringFunc(html, func(match string) string {
		groups := imgSrcRE.FindStringSubmatch(match)
		if len(groups) != 4 {
			return match
		}
		abs := resolveLocalSrc(groups[2], baseDir)
		if abs == "" {
			return match
		}
		out, ok := build(abs)
		if !ok {
			return match
		}
		return groups[1] + gohtml.EscapeString(out) + groups[3]
	})
}

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

func resolveLocalSrc(src, baseDir string) string {
	if src == "" || strings.HasPrefix(src, "//") || hasURLScheme(src) {
		return ""
	}
	clean := gohtml.UnescapeString(src)
	if i := strings.IndexAny(clean, "?#"); i >= 0 {
		clean = clean[:i]
	}
	if dec, err := url.PathUnescape(clean); err == nil {
		clean = dec
	}
	if filepath.IsAbs(clean) {
		return filepath.Clean(clean)
	}
	return filepath.Clean(filepath.Join(baseDir, clean))
}

func hasURLScheme(s string) bool {
	for i, c := range s {
		switch {
		case i == 0:
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
				return false
			}
		case c == ':':
			return i > 0
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.':
		default:
			return false
		}
	}
	return false
}

func FileURL(absPath string) string {
	u := url.URL{Scheme: "file", Path: absPath}
	return u.String()
}

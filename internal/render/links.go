package render

import (
	gohtml "html"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var imgSrcRE = regexp.MustCompile(`(<img\b[^>]*\bsrc=")([^"]+)(")`)

// RewriteImgSrc rewrites every local <img src="..."> in gohtml. build is
// called with the absolute filesystem path the src resolves to relative
// to baseDir; ok=false preserves the original src so callers can refuse
// out-of-tree paths without breaking sibling imgs.
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

// FileURL returns a percent-encoded file:// URL for absPath.
func FileURL(absPath string) string {
	u := url.URL{Scheme: "file", Path: absPath}
	return u.String()
}

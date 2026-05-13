package render

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	gohtml "html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/aldevv/md-preview/internal/render/pandoc"
)

// StaticTreeMaxFiles caps the BFS so a pathological linkfarm can't
// run the renderer hundreds of times during a single `mdp foo.md`.
// Over-cap links rewrite to a toast-sentinel so the user still gets
// a clean message instead of a broken navigation.
const StaticTreeMaxFiles = 200

// StaticTreeOptions configures RenderStaticTree. Mirrors the subset
// of BuildPage args the entry and its siblings need.
type StaticTreeOptions struct {
	Theme    string
	ExtraCSS string
	Colemak  bool
	// MaxFiles overrides StaticTreeMaxFiles when nonzero. Tests use
	// this to exercise the cap path without authoring 200 fixtures.
	MaxFiles int
}

// TmpHTMLPath returns the stable per-source tmp HTML file used by
// the static link-graph walker (and the single-file `mdp <file>`
// path that doesn't traverse). sha1 of the absolute source path
// keeps cross-file collisions out and lets re-runs overwrite in
// place rather than accumulate.
func TmpHTMLPath(tmpDir, source string) string {
	abs, err := filepath.Abs(source)
	if err != nil {
		abs = source
	}
	sum := sha1.Sum([]byte(abs))
	return filepath.Join(tmpDir, "mdp-"+hex.EncodeToString(sum[:])[:12]+".html")
}

// RenderStaticTree pre-renders entry plus every reachable .md file
// inside filepath.Dir(entry) (BFS, capped at MaxFiles). Each
// rendered .md gets its own TmpHTMLPath; outgoing links between
// them rewrite to file:// URLs so a static-mode preview navigates
// without a server. Links to non-md / out-of-tree / missing files
// become javascript:mdpStaticToast(...) sentinels that pop a toast
// instead of broken navigations. Returns the entry's tmp HTML path.
func RenderStaticTree(entry, tmpDir string, opts StaticTreeOptions) (string, error) {
	absEntry, err := filepath.Abs(entry)
	if err != nil {
		return "", err
	}
	rootDir := filepath.Dir(absEntry)
	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = StaticTreeMaxFiles
	}

	// BFS through .md links inside rootDir. bodies[abs] holds the
	// goldmark output for each rendered file before link rewriting.
	bodies := map[string]string{}
	queue := []string{absEntry}
	for len(queue) > 0 && len(bodies) < maxFiles {
		cur := queue[0]
		queue = queue[1:]
		if _, seen := bodies[cur]; seen {
			continue
		}
		body, err := RenderBody(cur)
		if err != nil {
			if cur == absEntry {
				return "", err
			}
			// Sub-files that fail to render get a small inline error
			// rather than aborting the whole walk.
			body = `<p>Error rendering ` + gohtml.EscapeString(cur) + `: ` + gohtml.EscapeString(err.Error()) + `</p>`
		}
		bodies[cur] = body
		for _, href := range extractLinkHrefs(body) {
			tgt := resolveHrefTarget(href, filepath.Dir(cur))
			if tgt == "" {
				continue
			}
			if !pathInsideDir(tgt, rootDir) {
				continue
			}
			ext := strings.ToLower(filepath.Ext(tgt))
			if ext != ".md" && ext != ".markdown" {
				continue
			}
			if _, statErr := os.Stat(tgt); statErr != nil {
				continue
			}
			queue = append(queue, filepath.Clean(tgt))
		}
	}

	// rendered maps abs source path → tmp HTML path. Built once so
	// link rewriting in every body sees the same mapping.
	rendered := make(map[string]string, len(bodies))
	for src := range bodies {
		rendered[src] = TmpHTMLPath(tmpDir, src)
	}

	for src, body := range bodies {
		rewritten := RewriteStaticLinks(body, src, rootDir, rendered)
		page := BuildPage(rewritten, opts.Theme, 0, opts.ExtraCSS, opts.Colemak, src)
		if err := writeStaticTmpFile(rendered[src], []byte(page)); err != nil {
			return "", err
		}
	}
	return rendered[absEntry], nil
}

// linkHrefRe matches `<a href="..."` in goldmark or pandoc HTML
// output. Both renderers emit lowercase tag + double-quoted attrs,
// so we don't bother with single-quoted or unquoted forms.
var linkHrefRe = regexp.MustCompile(`(<a\b[^>]*?\shref=)"([^"]*)"`)

// schemeRe matches an absolute-URI scheme prefix (`http:`, `mailto:`,
// `javascript:`, etc.) so the rewriter can let the browser handle
// those clicks instead of intercepting.
var schemeRe = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`)

// extractLinkHrefs returns every href= value from the rendered body
// for BFS queue purposes. Hrefs are returned as-is (un-resolved).
func extractLinkHrefs(body string) []string {
	matches := linkHrefRe.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[2])
	}
	return out
}

// resolveHrefTarget resolves href against srcDir if relative; returns
// "" for hrefs the static walker should ignore (anchors, schemes).
func resolveHrefTarget(href, srcDir string) string {
	if href == "" || strings.HasPrefix(href, "#") || schemeRe.MatchString(href) {
		return ""
	}
	if filepath.IsAbs(href) {
		return filepath.Clean(href)
	}
	return filepath.Clean(filepath.Join(srcDir, href))
}

// RewriteStaticLinks rewrites every <a href> in body for static-mode
// use:
//   - Anchor and external-scheme hrefs pass through unchanged.
//   - .md inside rootDir that we pre-rendered → file://<tmp html>.
//   - .md inside rootDir but missing from rendered (over-cap or
//     unreachable from entry) → toast sentinel.
//   - Non-md renderable extensions → toast sentinel (need `mdp
//     watch` to navigate).
//   - Non-renderable, inside rootDir → file:// to the source file
//     (browser does whatever it does with images, PDFs, …).
//   - Anything out of tree → toast sentinel.
//   - Missing files → toast sentinel.
func RewriteStaticLinks(body, srcAbs, rootDir string, rendered map[string]string) string {
	srcDir := filepath.Dir(srcAbs)
	return linkHrefRe.ReplaceAllStringFunc(body, func(match string) string {
		m := linkHrefRe.FindStringSubmatch(match)
		newHref := rewriteOneStaticHref(m[2], srcDir, rootDir, rendered)
		return m[1] + `"` + gohtml.EscapeString(newHref) + `"`
	})
}

func rewriteOneStaticHref(href, srcDir, rootDir string, rendered map[string]string) string {
	if href == "" || strings.HasPrefix(href, "#") || schemeRe.MatchString(href) {
		return href
	}
	target := href
	if !filepath.IsAbs(target) {
		target = filepath.Join(srcDir, target)
	}
	target = filepath.Clean(target)
	if !pathInsideDir(target, rootDir) {
		return staticToastHref("out of tree: " + href)
	}
	info, statErr := os.Stat(target)
	if statErr != nil || info.IsDir() {
		return staticToastHref("file not found: " + href)
	}
	ext := strings.ToLower(filepath.Ext(target))
	if ext == ".md" || ext == ".markdown" {
		if tmp, ok := rendered[target]; ok {
			return "file://" + tmp
		}
		return staticToastHref("not pre-rendered (max files reached): " + href)
	}
	if pandoc.InputFormat(target) != "" {
		return staticToastHref("not available in static mode (run 'mdp watch'): " + href)
	}
	return "file://" + target
}

// staticToastHref encodes msg as a URI-component string and wraps it
// in a javascript: URL that calls mdpStaticToast (declared in the
// page template). url.QueryEscape produces pure ASCII so the result
// is safe inside an HTML attribute value (no further escaping needed
// at the JS layer, single-quoted, no quote in the encoded form).
func staticToastHref(msg string) string {
	return fmt.Sprintf("javascript:mdpStaticToast('%s')", url.QueryEscape(msg))
}

// pathInsideDir reports whether cleanPath is rooted at dir. Both
// arguments must be absolute and clean. Local copy of the same-named
// helper in internal/server so the render package stays independent.
func pathInsideDir(cleanPath, dir string) bool {
	rel, err := filepath.Rel(dir, cleanPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// writeStaticTmpFile mirrors the o_NOFOLLOW write in cmd/mdp/main.go
// so a shared-tmp symlink attack can't aim a write at a foreign
// file. The render package can't import cmd/mdp, so it has its own
// copy.
func writeStaticTmpFile(path string, data []byte) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC | syscall.O_NOFOLLOW
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

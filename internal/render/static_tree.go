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
	"runtime"
	"strings"
	"sync"
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
// the static link-graph walker. sha1 of the symlink-resolved abs
// source path keeps two callers of the same physical file from
// computing different tmp filenames (e.g. macOS /var/folders vs
// /private/var/folders, or a sibling symlink to the same target).
// Falls back to filepath.Abs when EvalSymlinks fails so callers can
// still derive a path before the file exists.
func TmpHTMLPath(tmpDir, source string) string {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		if abs, aerr := filepath.Abs(source); aerr == nil {
			resolved = abs
		} else {
			resolved = source
		}
	}
	sum := sha1.Sum([]byte(resolved))
	return filepath.Join(tmpDir, "mdp-"+hex.EncodeToString(sum[:])[:12]+".html")
}

// RenderStaticTree pre-renders entry plus every reachable walkable
// file (markdown via goldmark, anything pandoc accepts via pandoc)
// inside filepath.Dir(entry). BFS is capped at MaxFiles and renders
// each wave's batch in parallel across runtime.NumCPU() workers.
// Outgoing links between rendered files rewrite to file:// URLs so a
// static-mode preview navigates without a server. Out-of-tree,
// missing, over-cap, and symlink-escape links rewrite to a
// javascript:mdpStaticToast(...) sentinel. Returns the entry's tmp
// HTML path.
func RenderStaticTree(entry, tmpDir string, opts StaticTreeOptions) (string, error) {
	absEntry, err := filepath.Abs(entry)
	if err != nil {
		return "", err
	}
	resolvedEntry, err := filepath.EvalSymlinks(absEntry)
	if err != nil {
		return "", err
	}
	rootDir, err := filepath.EvalSymlinks(filepath.Dir(absEntry))
	if err != nil {
		return "", err
	}
	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = StaticTreeMaxFiles
	}

	// Wave-parallel BFS through walkable links inside rootDir.
	// bodies[resolved] holds the rendered output keyed on the
	// symlink-resolved path. Each wave renders its batch in parallel
	// (pandoc subprocesses are the bottleneck); hrefs discovered
	// across the wave feed the next.
	bodies := map[string]string{}
	seen := map[string]bool{resolvedEntry: true}
	queue := []string{resolvedEntry}
	for len(queue) > 0 && len(bodies) < maxFiles {
		batch := queue
		queue = nil
		if room := maxFiles - len(bodies); len(batch) > room {
			batch = batch[:room]
		}
		results, err := renderBatch(batch, resolvedEntry)
		if err != nil {
			return "", err
		}
		for _, r := range results {
			bodies[r.path] = r.body
			for _, href := range extractLinkHrefs(r.body) {
				resolved := resolveWalkTarget(href, filepath.Dir(r.path), rootDir)
				if resolved == "" || seen[resolved] {
					continue
				}
				seen[resolved] = true
				queue = append(queue, resolved)
			}
		}
	}

	rendered := make(map[string]string, len(bodies))
	for src := range bodies {
		rendered[src] = TmpHTMLPath(tmpDir, src)
	}

	for src, body := range bodies {
		rewritten := RewriteStaticLinks(body, src, rootDir, rendered)
		rewritten = RewriteImgSrc(rewritten, filepath.Dir(src), func(abs string) (string, bool) {
			resolved, err := filepath.EvalSymlinks(abs)
			if err != nil || !pathInsideDir(resolved, rootDir) {
				return "", false
			}
			return FileURL(resolved), true
		})
		page := BuildPage(rewritten, opts.Theme, 0, opts.ExtraCSS, opts.Colemak, src)
		if err := writeStaticTmpFile(rendered[src], []byte(page)); err != nil {
			return "", err
		}
	}
	return rendered[resolvedEntry], nil
}

type renderResult struct {
	path string
	body string
	err  error
}

// renderBatch fans the paths out across runtime.NumCPU() workers and
// blocks until every render finishes. A failure on resolvedEntry
// aborts the whole walk; failures on sub-files fall through to an
// inline error body so one bad link doesn't take down the tree.
func renderBatch(paths []string, resolvedEntry string) ([]renderResult, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	workers := runtime.NumCPU()
	if workers > len(paths) {
		workers = len(paths)
	}
	jobs := make(chan string, len(paths))
	for _, p := range paths {
		jobs <- p
	}
	close(jobs)
	resultsCh := make(chan renderResult, len(paths))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				body, err := RenderBody(p)
				resultsCh <- renderResult{path: p, body: body, err: err}
			}
		}()
	}
	wg.Wait()
	close(resultsCh)

	out := make([]renderResult, 0, len(paths))
	for r := range resultsCh {
		if r.err != nil {
			if r.path == resolvedEntry {
				return nil, r.err
			}
			r.body = `<p>Error rendering ` + gohtml.EscapeString(r.path) + `: ` + gohtml.EscapeString(r.err.Error()) + `</p>`
		}
		out = append(out, r)
	}
	return out, nil
}

// resolveWalkTarget resolves href to a symlink-confined absolute path
// suitable for BFS enqueue. Returns "" when href is an anchor, an
// external scheme, an unwalkable extension, missing, or escapes
// rootDir (either lexically or via symlink). rootDir must already be
// symlink-resolved.
func resolveWalkTarget(href, srcDir, rootDir string) string {
	tgt := resolveHrefTarget(href, srcDir)
	if tgt == "" {
		return ""
	}
	if !pathInsideDir(tgt, rootDir) {
		return ""
	}
	if !isWalkableExt(tgt) {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(tgt)
	if err != nil {
		return ""
	}
	if !pathInsideDir(resolved, rootDir) {
		return ""
	}
	return resolved
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
//   - Walkable extension inside rootDir that we pre-rendered →
//     file://<tmp html>.
//   - Walkable extension inside rootDir but missing from rendered
//     (over-cap or unreachable from entry) → toast sentinel.
//   - Non-walkable, inside rootDir → file:// to the source file
//     (browser does whatever it does with images, PDFs, …).
//   - Anything out of tree (lexically or after symlink resolution) →
//     toast sentinel.
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
	resolved, rerr := filepath.EvalSymlinks(target)
	if rerr != nil {
		return staticToastHref("file not found: " + href)
	}
	info, statErr := os.Stat(resolved)
	if statErr != nil || info.IsDir() {
		return staticToastHref("file not found: " + href)
	}
	// Re-check after symlink resolution: a sibling inside rootDir
	// pointing at /etc/passwd would otherwise be served via file://.
	if !pathInsideDir(resolved, rootDir) {
		return staticToastHref("out of tree: " + href)
	}
	if isWalkableExt(resolved) {
		if tmp, ok := rendered[resolved]; ok {
			return "file://" + tmp
		}
		return staticToastHref("not pre-rendered (max files reached): " + href)
	}
	return "file://" + resolved
}

// isWalkableExt reports whether path's extension is one the static
// walker pre-renders: markdown via goldmark, anything else via
// pandoc. Other in-tree files fall through to a raw file:// link.
func isWalkableExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	}
	return pandoc.InputFormat(path) != ""
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

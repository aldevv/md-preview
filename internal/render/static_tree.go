package render

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	gohtml "html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/aldevv/md-preview/internal/osutil"
	"github.com/aldevv/md-preview/internal/render/pandoc"
)

// StaticTreeMaxFiles caps the BFS so a pathological linkfarm can't run
// the renderer hundreds of times during a single `mdp foo.md`.
const StaticTreeMaxFiles = 200

// StaticTreePandocBudget caps how many pandoc-renderable files the BFS
// walks. Pandoc renders are ~100-1000x more expensive than goldmark, so
// the total-files cap (StaticTreeMaxFiles) alone lets a .tex-heavy tree
// blow the wall-clock budget. Counted independently of StaticTreeMaxFiles.
const StaticTreePandocBudget = 25

type StaticTreeOptions struct {
	Theme    string
	ExtraCSS string
	Colemak  bool
	FileTree    bool
	FuzzyFinder bool
	// MaxFiles overrides StaticTreeMaxFiles when nonzero.
	MaxFiles int
	// PandocBudget overrides StaticTreePandocBudget when nonzero.
	PandocBudget int
}

// TmpHTMLPath returns the stable per-source tmp HTML file used by the
// static link-graph walker. EvalSymlinks dedups two hrefs at the same
// physical file (e.g. /var/folders vs /private/var/folders on macOS).
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

// RenderStaticTree pre-renders entry plus every walkable link inside
// filepath.Dir(entry), capped at MaxFiles. Each BFS wave renders in
// parallel; refused links (out-of-tree, missing, over-cap,
// symlink-escape) rewrite to a javascript:mdpStaticToast() sentinel.
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
	pandocBudget := opts.PandocBudget
	if pandocBudget <= 0 {
		pandocBudget = StaticTreePandocBudget
	}

	// bodies is keyed on the symlink-resolved path so two hrefs at the
	// same physical file dedup and an out-of-tree symlink target can't
	// slip past confinement. Each wave renders in parallel; discovered
	// hrefs feed the next.
	bodies := map[string]string{}
	seen := map[string]bool{resolvedEntry: true}
	queue := []string{resolvedEntry}
	pandocCount := 0
	if pandoc.InputFormat(resolvedEntry) != "" {
		pandocCount++
	}
	// FileTree and FuzzyFinder both want every previewable sibling clickable,
	// not just files reachable via <a href> from the entry. Seed the queue
	// with the whole walkable set so each one becomes a real pre-rendered
	// tmp HTML target; the BFS still discovers nothing new from there, it
	// just renders them. Budgets/caps still apply.
	if opts.FileTree || opts.FuzzyFinder {
		siblings, _ := WalkableFiles(rootDir, 0)
		for _, rel := range siblings {
			abs := filepath.Join(rootDir, filepath.FromSlash(rel))
			resolved, err := filepath.EvalSymlinks(abs)
			if err != nil || seen[resolved] {
				continue
			}
			if pandoc.InputFormat(resolved) != "" {
				if pandocCount >= pandocBudget {
					continue
				}
				pandocCount++
			}
			seen[resolved] = true
			queue = append(queue, resolved)
		}
	}
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
				if pandoc.InputFormat(resolved) != "" {
					if pandocCount >= pandocBudget {
						continue
					}
					pandocCount++
				}
				queue = append(queue, resolved)
			}
		}
	}

	rendered := make(map[string]string, len(bodies))
	for src := range bodies {
		rendered[src] = TmpHTMLPath(tmpDir, src)
	}

	treeJSON := ""
	if opts.FileTree || opts.FuzzyFinder {
		treeJSON = buildStaticTreeJSON(rootDir, rendered)
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
		page := BuildPage(rewritten, opts.Theme, 0, opts.ExtraCSS, opts.Colemak, src, opts.FileTree, opts.FuzzyFinder, treeJSON)
		if err := writeStaticTmpFile(rendered[src], []byte(page)); err != nil {
			return "", err
		}
	}
	return rendered[resolvedEntry], nil
}

// buildStaticTreeJSON walks the whole rootDir for files mdp can open
// and pairs each with its tmp HTML file path when one exists. Files we
// didn't pre-render (over-cap or unreachable from the entry) get an
// empty string; the JS tree click handler surfaces a toast for those.
// Errors are swallowed: the tree is a nice-to-have, not a hard
// dependency for serving the entry page.
func buildStaticTreeJSON(rootDir string, rendered map[string]string) string {
	files, err := WalkableFiles(rootDir, 0)
	if err != nil {
		return ""
	}
	renderedRel := make(map[string]string, len(rendered))
	for src, tmp := range rendered {
		rel, err := filepath.Rel(rootDir, src)
		if err != nil {
			continue
		}
		renderedRel[filepath.ToSlash(rel)] = FileURL(tmp)
	}
	payload := map[string]any{
		"root":     rootDir,
		"files":    files,
		"rendered": renderedRel,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(encoded)
}

type renderResult struct {
	path string
	body string
	err  error
}

// A failure on resolvedEntry aborts the whole walk; failures on
// sub-files fall through to an inline error body.
func renderBatch(paths []string, resolvedEntry string) ([]renderResult, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	workers := pandocParallelism(len(paths))
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

// MDP_PANDOC_PARALLELISM (positive int) overrides the default min(NumCPU, 8)
// cap. Result is clamped to [1, jobCount].
func pandocParallelism(jobCount int) int {
	if jobCount <= 0 {
		return 0
	}
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if v := os.Getenv("MDP_PANDOC_PARALLELISM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			workers = n
		}
	}
	if workers < 1 {
		workers = 1
	}
	if workers > jobCount {
		workers = jobCount
	}
	return workers
}

// rootDir must already be symlink-resolved.
func resolveWalkTarget(href, srcDir, rootDir string) string {
	tgt := resolveHrefTarget(href, srcDir)
	if tgt == "" {
		return ""
	}
	if !pathInsideDir(tgt, rootDir) {
		return ""
	}
	if !IsWalkableExt(tgt) {
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

var schemeRe = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`)

func extractLinkHrefs(body string) []string {
	matches := linkHrefRe.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[2])
	}
	return out
}

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
	if IsWalkableExt(resolved) {
		if tmp, ok := rendered[resolved]; ok {
			return "file://" + tmp
		}
		return staticToastHref("not pre-rendered (max files reached): " + href)
	}
	return "file://" + resolved
}

func IsWalkableExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	}
	return pandoc.InputFormat(path) != ""
}

// staticToastHref wraps msg in javascript:mdpStaticToast(...);
// url.QueryEscape produces pure ASCII so the result is safe inside the
// single-quoted JS string and the surrounding HTML attribute.
func staticToastHref(msg string) string {
	return fmt.Sprintf("javascript:mdpStaticToast('%s')", url.QueryEscape(msg))
}

// pathInsideDir reports whether cleanPath is rooted at dir. Both
// arguments must be absolute and clean.
func pathInsideDir(cleanPath, dir string) bool {
	rel, err := filepath.Rel(dir, cleanPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// O_NOFOLLOW defends against a shared-tmp symlink attack aiming our
// write at a foreign file.
func writeStaticTmpFile(path string, data []byte) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC | osutil.ONoFollow
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

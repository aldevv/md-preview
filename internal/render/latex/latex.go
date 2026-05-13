// Package latex emits client-side WASM placeholders for LaTeX in mdp
// previews and exposes the embedded pandoc-wasm bundle.
package latex

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	gohtml "html"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed wasm
var assets embed.FS

// Version pins the pandoc.wasm artifact this build embeds. Used to
// version the sibling-asset cache dir so multiple mdp installs can
// coexist and stale blobs are obvious. Bump in lockstep with the
// Makefile's PANDOC_WASM_VERSION.
const Version = "3.9.0.2"

func AssetsFS() fs.FS {
	sub, err := fs.Sub(assets, "wasm")
	if err != nil {
		panic(fmt.Errorf("latex: embed sub: %w", err))
	}
	return sub
}

// Placeholder wraps LaTeX source as a <div class="latex-pending">
// that the browser swaps with rendered HTML. The source is base64'd
// so newlines/quotes/braces survive the DOM attribute round-trip.
func Placeholder(src []byte, dataLine string) string {
	b64 := base64.StdEncoding.EncodeToString(src)
	var b strings.Builder
	b.WriteString(`<div class="latex-pending"`)
	if dataLine != "" {
		b.WriteString(` data-line="`)
		b.WriteString(gohtml.EscapeString(dataLine))
		b.WriteString(`"`)
	}
	b.WriteString(` data-src="`)
	b.WriteString(b64)
	b.WriteString(`">Rendering LaTeX…</div>`)
	return b.String()
}

func HasLatex(body string) bool {
	return strings.Contains(body, `class="latex-pending"`)
}

// WriteSiblingAssets ensures the WASM + JS bundle lives at
// <base>/mdp-pandoc-<Version>/ and returns that directory.
// Idempotent: files matching the embedded size are left alone, so
// re-runs across many mdp invocations are cheap.
func WriteSiblingAssets(base string) (string, error) {
	dir := filepath.Join(base, "mdp-pandoc-"+Version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	fsys := AssetsFS()
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := writeIfChanged(fsys, dir, e.Name()); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func writeIfChanged(fsys fs.FS, dir, name string) error {
	src, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, name)
	if cur, err := os.ReadFile(dst); err == nil && len(cur) == len(src) {
		return nil
	}
	return os.WriteFile(dst, src, 0o644)
}

// AssetsDigest returns a stable short hex digest of the embedded
// bundle. Useful to invalidate caches when the embed changes between
// mdp builds (e.g. a glue-script tweak that didn't bump Version).
func AssetsDigest() string {
	h := sha256.New()
	fsys := AssetsFS()
	entries, _ := fs.ReadDir(fsys, ".")
	for _, e := range entries {
		f, err := fsys.Open(e.Name())
		if err != nil {
			continue
		}
		_, _ = io.WriteString(h, e.Name())
		_, _ = io.Copy(h, f)
		f.Close()
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

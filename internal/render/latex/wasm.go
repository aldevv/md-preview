package latex

// WASM-mode fallback: when pandoc isn't on PATH, mdp embeds Pandoc's
// official wasm32-wasi build plus a JS bridge + WASI shim + DOMPurify.
// The HTML page includes a latex-pending placeholder; the browser
// loads pandoc.wasm and converts the LaTeX in-page. Slower (~2 s
// cold) but works without a host pandoc install.

import (
	"bytes"
	"compress/gzip"
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

// HasLatex reports whether body contains a WASM-mode placeholder.
// Used by the page builder to skip the WASM script bundle when the
// fast pandoc path was taken (or when there's no LaTeX at all).
func HasLatex(body string) bool {
	return strings.Contains(body, `class="latex-pending"`)
}

// WriteSiblingAssets writes the JS sidecars and a decompressed
// pandoc.wasm to <base>/mdp-pandoc-<Version>/ and returns that path.
// Idempotent: files of the expected content are left alone.
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
		if e.IsDir() || e.Name() == "pandoc.wasm.gz" {
			continue
		}
		if err := writeIfChanged(fsys, dir, e.Name()); err != nil {
			return "", err
		}
	}
	return dir, writeDecompressedWasm(fsys, dir)
}

func writeIfChanged(fsys fs.FS, dir, name string) error {
	src, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, name)
	if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, src) {
		return nil
	}
	return os.WriteFile(dst, src, 0o644)
}

// writeDecompressedWasm gunzips pandoc.wasm.gz at write time so the
// page can use WebAssembly.instantiateStreaming without paying a
// JS-side decompress per launch.
func writeDecompressedWasm(fsys fs.FS, dir string) error {
	dst := filepath.Join(dir, "pandoc.wasm")
	if info, err := os.Stat(dst); err == nil && info.Size() > 0 {
		return nil
	}
	gz, err := fsys.Open("pandoc.wasm.gz")
	if err != nil {
		return err
	}
	defer gz.Close()
	zr, err := gzip.NewReader(gz)
	if err != nil {
		return err
	}
	defer zr.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, zr)
	return err
}

// AssetsDigest returns a stable short hex digest of the embedded
// bundle. Useful to invalidate caches when the embed changes between
// mdp builds.
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

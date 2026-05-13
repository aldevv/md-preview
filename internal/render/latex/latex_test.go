package latex

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlaceholder(t *testing.T) {
	out := Placeholder([]byte("\\section{X}"), "7")
	for _, want := range []string{
		`class="latex-pending"`,
		`data-line="7"`,
		`data-src="`,
		`Rendering LaTeX`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("placeholder missing %q in %q", want, out)
		}
	}
}

func TestHasLatex(t *testing.T) {
	if !HasLatex(`<div class="latex-pending">x</div>`) {
		t.Errorf("HasLatex missed a pending div")
	}
	if HasLatex("<p>plain markdown</p>") {
		t.Errorf("HasLatex false-positive")
	}
}

func TestWriteSiblingAssets_WritesAllEmbedded(t *testing.T) {
	dir, err := WriteSiblingAssets(t.TempDir())
	if err != nil {
		t.Fatalf("WriteSiblingAssets: %v", err)
	}
	if filepath.Base(dir) != "mdp-pandoc-"+Version {
		t.Errorf("dir = %q, want suffix mdp-pandoc-%s", dir, Version)
	}
	entries, _ := fs.ReadDir(AssetsFS(), ".")
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("missing sibling %s: %v", e.Name(), err)
			continue
		}
		if info.IsDir() {
			t.Errorf("sibling %s is a directory", e.Name())
		}
	}
}

func TestWriteSiblingAssets_Idempotent(t *testing.T) {
	base := t.TempDir()
	dir, err := WriteSiblingAssets(base)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	wasmGz := filepath.Join(dir, "pandoc.wasm.gz")
	stat1, err := os.Stat(wasmGz)
	if err != nil {
		t.Fatalf("stat wasm.gz: %v", err)
	}

	if _, err := WriteSiblingAssets(base); err != nil {
		t.Fatalf("second call: %v", err)
	}
	stat2, err := os.Stat(wasmGz)
	if err != nil {
		t.Fatalf("stat wasm.gz after re-run: %v", err)
	}
	if !stat1.ModTime().Equal(stat2.ModTime()) {
		t.Errorf("idempotent call rewrote pandoc.wasm.gz (mtime changed)")
	}
}

package latex

import (
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

func TestWriteSiblingAssets_DecompressesWasm(t *testing.T) {
	dir, err := WriteSiblingAssets(t.TempDir())
	if err != nil {
		t.Fatalf("WriteSiblingAssets: %v", err)
	}
	if filepath.Base(dir) != "mdp-pandoc-"+Version {
		t.Errorf("dir = %q, want suffix mdp-pandoc-%s", dir, Version)
	}
	wasm := filepath.Join(dir, "pandoc.wasm")
	info, err := os.Stat(wasm)
	if err != nil {
		t.Fatalf("missing pandoc.wasm: %v", err)
	}
	if info.Size() < 1024*1024 {
		t.Errorf("pandoc.wasm = %d bytes, want > 1 MiB (decompressed)", info.Size())
	}
	head := make([]byte, 4)
	f, _ := os.Open(wasm)
	_, _ = f.Read(head)
	f.Close()
	if string(head) != "\x00asm" {
		t.Errorf("decompressed pandoc.wasm missing WASM magic, got %x", head)
	}
	if _, err := os.Stat(filepath.Join(dir, "pandoc.wasm.gz")); err == nil {
		t.Errorf("pandoc.wasm.gz unexpectedly written; file mode uses raw .wasm")
	}
	for _, name := range []string{"pandoc.js", "wasi-shim.js", "latex-render.js", "purify.min.js"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing sibling %s: %v", name, err)
		}
	}
}

func TestWriteSiblingAssets_Idempotent(t *testing.T) {
	base := t.TempDir()
	dir, err := WriteSiblingAssets(base)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	wasm := filepath.Join(dir, "pandoc.wasm")
	stat1, err := os.Stat(wasm)
	if err != nil {
		t.Fatalf("stat pandoc.wasm: %v", err)
	}

	if _, err := WriteSiblingAssets(base); err != nil {
		t.Fatalf("second call: %v", err)
	}
	stat2, err := os.Stat(wasm)
	if err != nil {
		t.Fatalf("stat pandoc.wasm after re-run: %v", err)
	}
	if !stat1.ModTime().Equal(stat2.ModTime()) {
		t.Errorf("idempotent call re-decompressed pandoc.wasm (mtime changed)")
	}
}

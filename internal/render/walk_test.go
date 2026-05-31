package render

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWalkableFiles_FiltersByExtAndSorts(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "b.md"))
	touch(t, filepath.Join(root, "a.md"))
	touch(t, filepath.Join(root, "ignored.txt"))
	touch(t, filepath.Join(root, "sub", "c.markdown"))
	touch(t, filepath.Join(root, "sub", "deeper", "d.md"))

	got, err := WalkableFiles(root, 0)
	if err != nil {
		t.Fatalf("WalkableFiles: %v", err)
	}
	want := []string{"a.md", "b.md", "sub/c.markdown", "sub/deeper/d.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWalkableFiles_SkipsNoiseDirs(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "keep.md"))
	for _, dir := range []string{".git", "node_modules", "vendor", "__pycache__", ".venv"} {
		touch(t, filepath.Join(root, dir, "lurking.md"))
	}
	touch(t, filepath.Join(root, "sub", "node_modules", "nested.md"))
	touch(t, filepath.Join(root, "sub", "also.md"))

	got, err := WalkableFiles(root, 0)
	if err != nil {
		t.Fatalf("WalkableFiles: %v", err)
	}
	want := []string{"keep.md", "sub/also.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWalkableFiles_RespectsCap(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md"} {
		touch(t, filepath.Join(root, name))
	}
	got, err := WalkableFiles(root, 2)
	if err != nil {
		t.Fatalf("WalkableFiles: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d files, want 2 (cap)", len(got))
	}
}

func TestWalkableFiles_RootedAtNoiseDirNameIsWalked(t *testing.T) {
	// The skip rule fires on descendant dirs only; if the root itself
	// happens to be named `node_modules`, we still walk into it.
	root := filepath.Join(t.TempDir(), "node_modules")
	touch(t, filepath.Join(root, "x.md"))
	got, err := WalkableFiles(root, 0)
	if err != nil {
		t.Fatalf("WalkableFiles: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"x.md"}) {
		t.Errorf("got %v, want [x.md]", got)
	}
}

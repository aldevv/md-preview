package render

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMD writes a markdown file under dir and returns its absolute
// path.
func writeMD(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestRenderStaticTree_FollowsMdLinks(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	a := writeMD(t, root, "a.md", "# A\n[b](b.md)\n[c](sub/c.md)\n")
	b := writeMD(t, root, "b.md", "# B\n[back](a.md)\n")
	c := writeMD(t, root, "sub/c.md", "# C\n[back](../a.md)\n")

	entry, err := RenderStaticTree(a, tmp, StaticTreeOptions{Theme: "dark"})
	if err != nil {
		t.Fatalf("RenderStaticTree: %v", err)
	}
	if entry != TmpHTMLPath(tmp, a) {
		t.Errorf("entry = %s, want %s", entry, TmpHTMLPath(tmp, a))
	}
	for _, src := range []string{a, b, c} {
		path := TmpHTMLPath(tmp, src)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected pre-rendered file for %s at %s: %v", src, path, err)
		}
	}
	// Entry's HTML should reference b's and c's tmp paths.
	entryHTML, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{b, c} {
		wantHref := "file://" + TmpHTMLPath(tmp, src)
		if !strings.Contains(string(entryHTML), wantHref) {
			t.Errorf("entry HTML missing rewritten link to %s (%s)", src, wantHref)
		}
	}
}

func TestRenderStaticTree_OutOfTreeBecomesToast(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	a := writeMD(t, root, "a.md", "# A\n[escape](../passwd)\n")

	entry, err := RenderStaticTree(a, tmp, StaticTreeOptions{Theme: "dark"})
	if err != nil {
		t.Fatalf("RenderStaticTree: %v", err)
	}
	body, _ := os.ReadFile(entry)
	if !strings.Contains(string(body), "mdpStaticToast") {
		t.Errorf("out-of-tree href should be a toast sentinel, got: %s", body)
	}
	if !strings.Contains(string(body), url.QueryEscape("out of tree")) {
		t.Errorf("toast payload missing 'out of tree' reason: %s", body)
	}
}

func TestRenderStaticTree_MissingFileBecomesToast(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	a := writeMD(t, root, "a.md", "# A\n[gone](nope.md)\n")

	entry, err := RenderStaticTree(a, tmp, StaticTreeOptions{Theme: "dark"})
	if err != nil {
		t.Fatalf("RenderStaticTree: %v", err)
	}
	body, _ := os.ReadFile(entry)
	if !strings.Contains(string(body), url.QueryEscape("file not found")) {
		t.Errorf("missing-file href should toast 'file not found': %s", body)
	}
}

func TestRenderStaticTree_NonMdRenderableBecomesToast(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	a := writeMD(t, root, "a.md", "# A\n[paper](paper.tex)\n")
	if err := os.WriteFile(filepath.Join(root, "paper.tex"), []byte(`\section{X}`), 0o600); err != nil {
		t.Fatal(err)
	}

	entry, err := RenderStaticTree(a, tmp, StaticTreeOptions{Theme: "dark"})
	if err != nil {
		t.Fatalf("RenderStaticTree: %v", err)
	}
	body, _ := os.ReadFile(entry)
	if !strings.Contains(string(body), url.QueryEscape("not available in static mode")) {
		t.Errorf("non-md href should toast 'not available in static mode': %s", body)
	}
}

func TestRenderStaticTree_NonRenderablePassesThroughAsFileURL(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	a := writeMD(t, root, "a.md", "# A\n[image](logo.png)\n")
	if err := os.WriteFile(filepath.Join(root, "logo.png"), []byte("fake-png"), 0o600); err != nil {
		t.Fatal(err)
	}

	entry, err := RenderStaticTree(a, tmp, StaticTreeOptions{Theme: "dark"})
	if err != nil {
		t.Fatalf("RenderStaticTree: %v", err)
	}
	body, _ := os.ReadFile(entry)
	wantHref := "file://" + filepath.Join(root, "logo.png")
	if !strings.Contains(string(body), wantHref) {
		t.Errorf("non-renderable in-tree href should pass through as %s: %s", wantHref, body)
	}
}

func TestRenderStaticTree_MaxFilesCap(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	a := writeMD(t, root, "a.md", "# A\n[b](b.md)\n")
	b := writeMD(t, root, "b.md", "# B\n[c](c.md)\n")
	c := writeMD(t, root, "c.md", "# C\n")
	_ = b
	_ = c

	// MaxFiles=1 means only entry gets rendered; the link to b
	// should fall through to the cap-reached toast sentinel.
	entry, err := RenderStaticTree(a, tmp, StaticTreeOptions{Theme: "dark", MaxFiles: 1})
	if err != nil {
		t.Fatalf("RenderStaticTree: %v", err)
	}
	body, _ := os.ReadFile(entry)
	if !strings.Contains(string(body), url.QueryEscape("max files reached")) {
		t.Errorf("over-cap href should toast 'max files reached': %s", body)
	}
	if _, err := os.Stat(TmpHTMLPath(tmp, b)); err == nil {
		t.Errorf("b.md should NOT have been pre-rendered under cap=1")
	}
}

func TestRenderStaticTree_ExternalAndAnchorLinksUntouched(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	a := writeMD(t, root, "a.md", "# A\n[ext](https://example.com)\n[anchor](#foo)\n")

	entry, err := RenderStaticTree(a, tmp, StaticTreeOptions{Theme: "dark"})
	if err != nil {
		t.Fatalf("RenderStaticTree: %v", err)
	}
	body, _ := os.ReadFile(entry)
	if !strings.Contains(string(body), `href="https://example.com"`) {
		t.Errorf("external link should pass through unchanged: %s", body)
	}
	if !strings.Contains(string(body), `href="#foo"`) {
		t.Errorf("anchor link should pass through unchanged: %s", body)
	}
}

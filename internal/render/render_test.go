package render

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldevv/md-preview/internal/render/latex"
)

func TestStripFrontmatter(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "with frontmatter",
			in:   "---\ntitle: t\n---\n# h\n",
			want: "# h\n",
		},
		{
			name: "no frontmatter",
			in:   "# h\nbody\n",
			want: "# h\nbody\n",
		},
		{
			name: "unclosed frontmatter",
			in:   "---\ntitle: t\nbody",
			want: "---\ntitle: t\nbody",
		},
		{
			name: "empty file",
			in:   "",
			want: "",
		},
		{
			name: "frontmatter at line 0 only",
			in:   "---\n---\n",
			want: "",
		},
		{
			name: "hr later in body is not stripped",
			in:   "# h\n\nbody\n\n---\n\nmore\n",
			want: "# h\n\nbody\n\n---\n\nmore\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripFrontmatter(tt.in)
			if got != tt.want {
				t.Errorf("stripFrontmatter()\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestRenderHeadings(t *testing.T) {
	out := RenderBytes([]byte("# h1\n"))
	if !strings.Contains(out, "<h1") {
		t.Errorf("missing <h1 in output: %q", out)
	}
	if !strings.Contains(out, `data-line="1"`) {
		t.Errorf(`missing data-line="1" in output: %q`, out)
	}
}

func TestRenderParagraphLineNumbers(t *testing.T) {
	src := "first paragraph\n\nsecond paragraph\n"
	out := RenderBytes([]byte(src))
	if !strings.Contains(out, `data-line="1"`) {
		t.Errorf(`expected data-line="1" for first paragraph: %q`, out)
	}
	if !strings.Contains(out, `data-line="3"`) {
		t.Errorf(`expected data-line="3" for second paragraph: %q`, out)
	}
}

func TestRenderTable(t *testing.T) {
	src := "| A | B |\n| - | - |\n| 1 | 2 |\n"
	out := RenderBytes([]byte(src))
	if !strings.Contains(out, "<table") {
		t.Errorf("missing <table in output: %q", out)
	}
	if !strings.Contains(out, "data-line=") {
		t.Errorf("expected at least one data-line attribute on table: %q", out)
	}
}

func TestRenderTable_RowAndCellAnnotated(t *testing.T) {
	src := "| A | B |\n| - | - |\n| 1 | 2 |\n"
	out := RenderBytes([]byte(src))
	for _, want := range []string{"<th", "<td", "<tr"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output: %q", want, out)
		}
	}
	count := strings.Count(out, "data-line=")
	if count < 5 {
		t.Errorf("expected data-line on table, header, row(s), cells (>=5); got %d in %q", count, out)
	}
}

func TestRenderListItems_EachItemAnnotated(t *testing.T) {
	src := "- one\n- two\n- three\n"
	out := RenderBytes([]byte(src))
	for _, want := range []string{`data-line="1"`, `data-line="2"`, `data-line="3"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in list output: %q", want, out)
		}
	}
}

// Raw HTML in markdown is intentionally NOT rendered — see render.go
// (WithUnsafe). These tests guard the security boundary.

func TestRenderHTMLBlock_RawHTMLOmitted(t *testing.T) {
	src := "para\n\n<details>\n<summary>x</summary>\nhi\n</details>\n"
	out := RenderBytes([]byte(src))
	if strings.Contains(out, "<details>") || strings.Contains(out, "<summary>") {
		t.Errorf("raw HTML should be omitted: %q", out)
	}
}

func TestRenderHTMLBlock_ScriptDropped(t *testing.T) {
	out := RenderBytes([]byte("para\n\n<script>window.evil=1</script>\n"))
	if strings.Contains(out, "<script") {
		t.Errorf("script tag must be stripped: %q", out)
	}
}

func TestRenderTaskList(t *testing.T) {
	src := "- [x] done\n- [ ] todo\n"
	out := RenderBytes([]byte(src))
	if !strings.Contains(out, "<input") {
		t.Errorf("missing <input in output: %q", out)
	}
	if !strings.Contains(out, `type="checkbox"`) {
		t.Errorf(`missing type="checkbox" in output: %q`, out)
	}
	if !strings.Contains(out, "checked") {
		t.Errorf("missing checked in output: %q", out)
	}
	if !strings.Contains(out, "data-line=") {
		t.Errorf("expected data-line on task list <li>: %q", out)
	}
}

func TestRenderLinkify(t *testing.T) {
	src := "Visit https://example.com today.\n"
	out := RenderBytes([]byte(src))
	if !strings.Contains(out, `<a href="https://example.com"`) {
		t.Errorf(`missing linkified anchor in output: %q`, out)
	}
}

func TestRenderFencedCode(t *testing.T) {
	src := "```go\npackage main\n```\n"
	out := RenderBytes([]byte(src))
	if !strings.Contains(out, `<code class="language-go"`) {
		t.Errorf("missing fenced code class in output: %q", out)
	}
	if !strings.Contains(out, `data-line="1"`) {
		t.Errorf(`expected data-line="1" on fenced code: %q`, out)
	}
}

// hideFromPath empties PATH for the duration of the test so the
// pandoc lookup miss path engages (emits a .latex-error div in
// fences, or returns ErrPandocNotFound from RenderBody).
func hideFromPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", "/nonexistent")
	latex.ResetPandocProbe()
}

func TestRenderFencedLatex_PandocRenders(t *testing.T) {
	if !latex.PandocAvailable() {
		t.Skip("pandoc not on PATH")
	}
	src := "Prose.\n\n```latex\n\\textbf{bold}\n```\n"
	out := RenderBytes([]byte(src))
	if !strings.Contains(out, `class="latex-block"`) {
		t.Errorf("expected latex-block (pandoc-rendered), got: %q", out)
	}
	if !strings.Contains(out, `<strong>bold</strong>`) {
		t.Errorf("expected <strong>bold</strong> from pandoc, got: %q", out)
	}
}

func TestRenderFencedLatex_PandocMissingShowsError(t *testing.T) {
	hideFromPath(t)
	src := "Prose.\n\n```latex\n\\section{X}\n```\n"
	out := RenderBytes([]byte(src))
	if !strings.Contains(out, `class="latex-error"`) {
		t.Errorf("expected .latex-error in fence output, got: %q", out)
	}
	if !strings.Contains(out, "pandoc not found") {
		t.Errorf("expected install hint in error message, got: %q", out)
	}
}

func TestRenderFencedNonLatex_StaysAsCodeBlock(t *testing.T) {
	src := "```python\nprint('hi')\n```\n"
	out := RenderBytes([]byte(src))
	if !strings.Contains(out, `class="language-python"`) {
		t.Errorf("non-latex fence should stay a code block, got: %q", out)
	}
	if strings.Contains(out, `class="latex-block"`) {
		t.Errorf("python fence got latex-routed, output: %q", out)
	}
}

func TestRenderBody_TexExtensionRequiresPandoc(t *testing.T) {
	hideFromPath(t)
	tmp := t.TempDir()
	path := tmp + "/sample.tex"
	if err := os.WriteFile(path, []byte(`\section{X}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := RenderBody(path)
	if err == nil {
		t.Fatalf("expected error for .tex without pandoc, got nil")
	}
	if !errors.Is(err, latex.ErrPandocNotFound) {
		t.Errorf("err = %v, want ErrPandocNotFound", err)
	}
}

func TestRenderFencedLatex_DataLinePreserved(t *testing.T) {
	if !latex.PandocAvailable() {
		t.Skip("pandoc not on PATH")
	}
	src := "prose\n\n```latex\n\\section{X}\n```\n"
	out := RenderBytes([]byte(src))
	if !strings.Contains(out, `class="latex-block"`) {
		t.Fatalf("expected latex-block wrapper: %q", out)
	}
	if !strings.Contains(out, `data-line="3"`) {
		t.Errorf(`expected data-line="3" on latex-block, got: %q`, out)
	}
}

func TestRenderHR(t *testing.T) {
	src := "para\n\n---\n\nmore\n"
	out := RenderBytes([]byte(src))
	if !strings.Contains(out, "<hr") {
		t.Errorf("missing <hr in output: %q", out)
	}
	if !strings.Contains(out, `data-line="3"`) {
		t.Errorf(`expected data-line="3" on hr: %q`, out)
	}
}

func TestRenderBlockquote(t *testing.T) {
	src := "> a quote\n"
	out := RenderBytes([]byte(src))
	if !strings.Contains(out, "<blockquote") {
		t.Errorf("missing <blockquote in output: %q", out)
	}
	if !strings.Contains(out, `data-line="1"`) {
		t.Errorf(`expected data-line="1" on blockquote: %q`, out)
	}
}

func TestRenderGitHubAlert(t *testing.T) {
	src := "> [!NOTE]\n> body text\n"
	out := RenderBytes([]byte(src))
	wants := []string{
		`<div class="markdown-alert markdown-alert-note"`,
		`<p class="markdown-alert-title">Note</p>`,
		"body text",
		`data-line="1"`,
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in alert output: %q", want, out)
		}
	}
	if strings.Contains(out, "[!NOTE]") {
		t.Errorf("[!NOTE] header should be stripped: %q", out)
	}
	if strings.Contains(out, "<blockquote") {
		t.Errorf("alert must not render as <blockquote>: %q", out)
	}
}

func TestRenderGitHubAlert_AllKinds(t *testing.T) {
	for _, kind := range []string{"note", "tip", "important", "warning", "caution"} {
		src := "> [!" + strings.ToUpper(kind) + "]\n> body\n"
		out := RenderBytes([]byte(src))
		want := `markdown-alert-` + kind
		if !strings.Contains(out, want) {
			t.Errorf("kind %q: missing %q in output: %q", kind, want, out)
		}
	}
}

func TestRenderBlockquote_UnknownAlertType(t *testing.T) {
	src := "> [!FOO]\n> body\n"
	out := RenderBytes([]byte(src))
	if !strings.Contains(out, "<blockquote") {
		t.Errorf("unknown alert type should fall back to plain blockquote: %q", out)
	}
	if !strings.Contains(out, "[!FOO]") {
		t.Errorf("unknown alert tag should remain in body: %q", out)
	}
}

func samplePath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..", "testdata", "sample.md")
}

func TestRenderBody_File(t *testing.T) {
	path := samplePath(t)
	body, err := RenderBody(path)
	if err != nil {
		t.Fatalf("RenderBody: %v", err)
	}

	wants := []string{
		"Sample Document",
		"<h1",
		"<table",
		`<code class="language-go"`,
		`type="checkbox"`,
		"<blockquote",
		"<hr",
		`<a href="https://example.com"`,
		"data-line=",
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody: %s", want, body)
		}
	}
}

func TestRenderBody_MissingFile(t *testing.T) {
	body, err := RenderBody(filepath.Join(t.TempDir(), "does-not-exist.md"))
	if err == nil {
		t.Fatal("expected non-nil error for missing file")
	}
	if !strings.Contains(body, "Error reading file:") {
		t.Errorf("body missing error marker: %q", body)
	}
}

func TestBuildPage_DarkTheme(t *testing.T) {
	page := BuildPage("<p>x</p>", "dark", 0, "", false)
	wants := []string{
		"--color-bg-primary: #0d1117",
		// dark hljs theme: code background is #0d1117
		"pre code.hljs",
		"background:#0d1117",
		// inline highlight.js, not a CDN link
		"var hljs=function()",
		"case 'j':",
	}
	for _, want := range wants {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(page, "cdnjs.cloudflare.com") {
		t.Errorf("page must not reference cdnjs.cloudflare.com (assets are embedded)")
	}
}

func TestBuildPage_LightTheme(t *testing.T) {
	page := BuildPage("<p>x</p>", "light", 0, "", false)
	wants := []string{
		"--color-bg-primary: #ffffff",
		// light hljs theme: code background is #fff
		"pre code.hljs",
		"background:#fff",
		"var hljs=function()",
	}
	for _, want := range wants {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(page, "cdnjs.cloudflare.com") {
		t.Errorf("page must not reference cdnjs.cloudflare.com (assets are embedded)")
	}
}

func TestBuildPage_NoWS(t *testing.T) {
	page := BuildPage("<p>x</p>", "dark", 0, "", false)
	if strings.Contains(page, "new WebSocket") {
		t.Errorf("expected no WebSocket script when wsPort=0; page contains it")
	}
}

func TestBuildPage_WithWS(t *testing.T) {
	page := BuildPage("<p>x</p>", "dark", 8765, "", false)
	if !strings.Contains(page, "new WebSocket('ws://localhost:8765/ws')") {
		t.Errorf("page missing WebSocket connect string for port 8765")
	}
}

func TestBuildPage_ExtraCSS(t *testing.T) {
	marker := "body { font-size: 42px; }"
	page := BuildPage("<p>x</p>", "dark", 0, marker, false)
	idxExtra := strings.Index(page, marker)
	idxCommon := strings.Index(page, ".markdown-body h1 {")
	if idxExtra < 0 {
		t.Fatalf("extraCSS marker not found in page")
	}
	if idxCommon < 0 {
		t.Fatalf("default common CSS marker not found in page")
	}
	if idxExtra <= idxCommon {
		t.Errorf("extraCSS should appear after default CSS for cascade-correct ordering")
	}
}

func TestBuildPage_VimKeys(t *testing.T) {
	t.Run("noWS", func(t *testing.T) {
		page := BuildPage("<p>x</p>", "dark", 0, "", false)
		if !strings.Contains(page, "case 'j':") {
			t.Errorf("vim-keys script missing when wsPort=0")
		}
	})
	t.Run("withWS", func(t *testing.T) {
		page := BuildPage("<p>x</p>", "dark", 8765, "", false)
		if !strings.Contains(page, "case 'j':") {
			t.Errorf("vim-keys script missing when wsPort>0")
		}
	})
}

func TestBuildPage_QuitKey(t *testing.T) {
	for _, colemak := range []bool{false, true} {
		page := BuildPage("<p>x</p>", "dark", 0, "", colemak)
		if !strings.Contains(page, "case 'q': window.close();") {
			t.Errorf("page missing q→close binding (colemak=%v)", colemak)
		}
	}
}

func TestBuildPage_Colemak(t *testing.T) {
	page := BuildPage("<p>x</p>", "dark", 0, "", true)
	wants := []string{"case 'n':", "case 'e':", "case 'i':", "case 'h':"}
	for _, want := range wants {
		if !strings.Contains(page, want) {
			t.Errorf("colemak page missing %q", want)
		}
	}
	bad := []string{"case 'j':", "case 'k':", "case 'l':"}
	for _, b := range bad {
		if strings.Contains(page, b) {
			t.Errorf("colemak page should not contain %q", b)
		}
	}
}

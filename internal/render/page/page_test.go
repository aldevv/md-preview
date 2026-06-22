package page

import (
	"strings"
	"testing"
)

func TestBuildPage_DarkTheme(t *testing.T) {
	page := BuildPage(PageOptions{Body: `<pre><code class="language-go">x</code></pre>`, Theme: "dark"})
	wants := []string{
		"--color-bg-primary: #0d1117",
		"pre code.hljs",
		"background:#0d1117",
		"var hljs=function()",
		"isKey(e, 'down')",
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
	page := BuildPage(PageOptions{Body: `<pre><code class="language-go">x</code></pre>`, Theme: "light"})
	wants := []string{
		"--color-bg-primary: #ffffff",
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

func TestBuildPage_OmitsHljsForProse(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>just prose, no fences</p>", Theme: "dark"})
	for _, bad := range []string{"var hljs=function()", "hljs.highlightAll();", "pre code.hljs"} {
		if strings.Contains(page, bad) {
			t.Errorf("prose-only page should omit %q", bad)
		}
	}
}

func TestBuildPage_IncludesHljsForCodeFence(t *testing.T) {
	page := BuildPage(PageOptions{Body: `<pre><code class="language-go">package main</code></pre>`, Theme: "dark"})
	for _, want := range []string{"var hljs=function()", "hljs.highlightAll();", "pre code.hljs"} {
		if !strings.Contains(page, want) {
			t.Errorf("code-fence page should include %q", want)
		}
	}
}

func TestBuildPage_NoWS(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark"})
	if strings.Contains(page, "new WebSocket") {
		t.Errorf("expected no WebSocket script when wsPort=0; page contains it")
	}
}

func TestBuildPage_WithWS(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", WSPort: 8765})
	if !strings.Contains(page, "new WebSocket('ws://localhost:8765/ws')") {
		t.Errorf("page missing WebSocket connect string for port 8765")
	}
}

func TestBuildPage_ExtraCSS(t *testing.T) {
	marker := "body { font-size: 42px; }"
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", ExtraCSS: marker})
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
		page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark"})
		if !strings.Contains(page, "isKey(e, 'down')") || !strings.Contains(page, `"down":"j"`) {
			t.Errorf("vim-keys script missing when wsPort=0")
		}
	})
	t.Run("withWS", func(t *testing.T) {
		page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", WSPort: 8765})
		if !strings.Contains(page, "isKey(e, 'down')") || !strings.Contains(page, `"down":"j"`) {
			t.Errorf("vim-keys script missing when wsPort>0")
		}
	})
}

func TestBuildPage_QuitKey(t *testing.T) {
	for _, colemak := range []bool{false, true} {
		page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Colemak: colemak})
		if !strings.Contains(page, `"close":"q"`) || !strings.Contains(page, "window.close();") {
			t.Errorf("page missing q→close binding (colemak=%v)", colemak)
		}
	}
}

func TestBuildPage_ReloadKey(t *testing.T) {
	want := "else if (isKey(e, 'reload'))"
	t.Run("static", func(t *testing.T) {
		page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark"})
		if !strings.Contains(page, want) {
			t.Errorf("static page missing r→reload binding")
		}
	})
	t.Run("withWS", func(t *testing.T) {
		page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", WSPort: 8765})
		if strings.Contains(page, want) {
			t.Errorf("WS-backed page should not bind r→reload (watch/serve drive their own refresh)")
		}
	})
}

func TestBuildPage_HasExternalLinkHandler(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark"})
	wants := []string{
		"/^(https?|mailto|tel|ftp|ftps):/i",
		"window.open(href, '_blank', 'noopener,noreferrer')",
		"e.preventDefault()",
		"if (e.defaultPrevented) return;",
	}
	for _, want := range wants {
		if !strings.Contains(page, want) {
			t.Errorf("page missing external-link handler fragment %q", want)
		}
	}
}

func TestBuildPage_FileTreeDisabled(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark"})
	bad := []string{
		`id="mdp-tree"`,
		"mdpToggleTree",
		"mdpEnsureTreeData",
		"window.mdpStaticTree =",
	}
	for _, b := range bad {
		if strings.Contains(page, b) {
			t.Errorf("disabled-tree page should not contain %q", b)
		}
	}
}

func TestBuildPage_FileTreeEnabled(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", FileTree: true})
	wants := []string{
		`id="mdp-tree"`,
		`class="mdp-tree-body"`,
		"mdpToggleTree",
		"mdpEnsureTreeData",
		`e.key === "Tab"`,
		`e.key === "Enter"`,
		`e.key === 'ArrowDown' || e.key === "j"`,
		`e.key === 'ArrowUp' || e.key === "k"`,
		`e.key === 'ArrowRight' || e.key === "l"`,
		`e.key === 'ArrowLeft' || e.key === "h"`,
	}
	for _, w := range wants {
		if !strings.Contains(page, w) {
			t.Errorf("enabled-tree page missing %q", w)
		}
	}
	if strings.Contains(page, "window.mdpStaticTree =") {
		t.Errorf("WS-mode page (empty static JSON) should not embed a window.mdpStaticTree assignment")
	}
	if strings.Contains(page, `id="mdp-finder"`) {
		t.Errorf("file-tree-only page should not include the finder DOM")
	}
}

func TestBuildPage_FileTreeColemakKeys(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Colemak: true, FileTree: true})
	wants := []string{
		`e.key === 'ArrowDown' || e.key === "n"`,
		`e.key === 'ArrowUp' || e.key === "e"`,
		`e.key === 'ArrowRight' || e.key === "i"`,
		`e.key === 'ArrowLeft' || e.key === "h"`,
	}
	for _, w := range wants {
		if !strings.Contains(page, w) {
			t.Errorf("colemak tree page missing %q", w)
		}
	}
	for _, bad := range []string{"__TREE_DOWN__", "__TREE_UP__", "__TREE_RIGHT__", `e.key === 'ArrowDown' || e.key === "j"`, `e.key === 'ArrowUp' || e.key === "k"`, `e.key === 'ArrowRight' || e.key === "l"`} {
		if strings.Contains(page, bad) {
			t.Errorf("colemak tree page should not contain %q", bad)
		}
	}
}

func TestBuildPage_FileTreeStaticData(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", FileTree: true, StaticTreeJSON: `{"root":"/r","files":["a.md"]}`})
	wants := []string{
		`window.mdpStaticTree = {"root":"/r","files":["a.md"]};`,
		"mdpToggleTree",
	}
	for _, w := range wants {
		if !strings.Contains(page, w) {
			t.Errorf("static-tree page missing %q", w)
		}
	}
}

func TestBuildPage_FuzzyFinderEnabledStandalone(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", FuzzyFinder: true})
	wants := []string{
		`id="mdp-finder"`,
		`id="mdp-finder-input"`,
		`id="mdp-finder-list"`,
		"mdpFuzzyMatch",
		"mdpFinderOpen",
		"mdpEnsureTreeData",
		`mdpMatchesKeySpec(e, "Ctrl+p")`,
	}
	for _, w := range wants {
		if !strings.Contains(page, w) {
			t.Errorf("finder-enabled page missing %q", w)
		}
	}
	if strings.Contains(page, `id="mdp-tree"`) {
		t.Errorf("finder-only page should not include the tree DOM")
	}
}

func TestBuildPage_FuzzyFinderOmittedByDefault(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark"})
	for _, bad := range []string{`id="mdp-finder"`, "mdpFuzzyMatch", "mdpFinderOpen", "mdpEnsureTreeData"} {
		if strings.Contains(page, bad) {
			t.Errorf("page with all nav gates off should not contain %q", bad)
		}
	}
}

func TestBuildPage_FuzzyFinderAndTreeShareData(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", FileTree: true, FuzzyFinder: true, StaticTreeJSON: `{"root":"/r","files":["a.md"]}`})
	wants := []string{
		`id="mdp-tree"`,
		`id="mdp-finder"`,
		`window.mdpStaticTree = {"root":"/r","files":["a.md"]};`,
		"mdpEnsureTreeData",
	}
	for _, w := range wants {
		if !strings.Contains(page, w) {
			t.Errorf("combined page missing %q", w)
		}
	}
	if c := strings.Count(page, "function mdpEnsureTreeData"); c != 1 {
		t.Errorf("mdpEnsureTreeData should appear exactly once, got %d", c)
	}
}

func TestBuildPage_SearchPanelPresent(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark"})
	for _, want := range []string{
		`id="mdp-search"`,
		`id="mdp-search-input"`,
		`id="mdp-search-count"`,
		"mdpSearchIsOpen",
		"mdpSearchCurrentTerm",
		`OPEN_KEY = "/"`,
		`NEXT_KEY = "n"`,
		`PREV_KEY = "N"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("search-enabled page missing %q", want)
		}
	}
}

func TestBuildPage_SearchKeys_Colemak(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Colemak: true})
	for _, want := range []string{`NEXT_KEY = "k"`, `PREV_KEY = "K"`} {
		if !strings.Contains(page, want) {
			t.Errorf("colemak search keys missing %q", want)
		}
	}
}

func TestBuildPage_NavHistoryKeys_Qwerty(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark"})
	for _, want := range []string{`"history_back":"H"`, `"history_forward":"L"`, "mdpGoBack()", "mdpGoForward()"} {
		if !strings.Contains(page, want) {
			t.Errorf("qwerty page missing %q", want)
		}
	}
	if strings.Contains(page, `"history_forward":"I"`) {
		t.Errorf("qwerty page should not bind 'I' (that's the colemak forward)")
	}
}

func TestBuildPage_NavHistoryKeys_Colemak(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Colemak: true})
	for _, want := range []string{`"history_back":"H"`, `"history_forward":"I"`, "mdpGoBack()", "mdpGoForward()"} {
		if !strings.Contains(page, want) {
			t.Errorf("colemak page missing %q", want)
		}
	}
	if strings.Contains(page, `"history_forward":"L"`) {
		t.Errorf("colemak page should not bind 'L' (qwerty-only forward)")
	}
}

func TestBuildPage_Colemak(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Colemak: true})
	wants := []string{`"down":"n"`, `"up":"e"`, `"right":"i"`, `"left":"h"`}
	for _, want := range wants {
		if !strings.Contains(page, want) {
			t.Errorf("colemak page missing %q", want)
		}
	}
	bad := []string{`"down":"j"`, `"up":"k"`, `"right":"l"`}
	for _, b := range bad {
		if strings.Contains(page, b) {
			t.Errorf("colemak page should not contain %q", b)
		}
	}
}

func TestBuildPageWithKeys_CustomOverrides(t *testing.T) {
	page := BuildPage(PageOptions{
		Body: "<p>x</p>", Theme: "dark",
		FileTree: true, FuzzyFinder: true,
		KeyOverrides: map[string]string{
			"down":        "s",
			"tree_toggle": "t",
			"tree_open":   "o",
			"finder_open": "Ctrl+o",
			"bogus":       "x",
		},
	})
	for _, want := range []string{
		`"down":"s"`,
		`e.key === "t"`,
		`e.key === "o"`,
		`mdpMatchesKeySpec(e, "Ctrl+o")`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("custom-key page missing %q", want)
		}
	}
	if strings.Contains(page, `"bogus":"x"`) {
		t.Errorf("unknown key action should not be emitted")
	}
}

func TestBuildPage_HopOnly(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Hop: true})
	for _, want := range []string{"mdpSelectStartPick", "HOP_ENABLED = true", `"select_pick":"s"`} {
		if !strings.Contains(page, want) {
			t.Errorf("hop-only page missing %q", want)
		}
	}
	if strings.Contains(page, "HOP_ENABLED = false") || strings.Contains(page, "VISUAL_ENABLED = true") {
		t.Errorf("hop-only page has wrong select gate")
	}
}

func TestBuildPage_VisualOnly(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Visual: true})
	for _, want := range []string{"mdpSelectStartCenter", "VISUAL_ENABLED = true", `"select_visual":"v"`} {
		if !strings.Contains(page, want) {
			t.Errorf("visual-only page missing %q", want)
		}
	}
	if strings.Contains(page, "HOP_ENABLED = true") {
		t.Errorf("visual-only page should not enable hop")
	}
}

func TestBuildPage_HopAndVisual(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Hop: true, Visual: true})
	for _, want := range []string{"mdpSelectStartPick", "mdpSelectStartCenter", "mdpSelectPaintLineNumbers", "HOP_ENABLED = true", "VISUAL_ENABLED = true"} {
		if !strings.Contains(page, want) {
			t.Errorf("hop+visual page missing %q", want)
		}
	}
}

func TestBuildPage_SelectModeOmitted(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark"})
	for _, bad := range []string{"mdpSelectStartPick", "mdpSelectStartCenter", "mdpSelectPaintLineNumbers", "__VISUAL_SELECT_SCRIPT__"} {
		if strings.Contains(page, bad) {
			t.Errorf("select-disabled page should not contain %q", bad)
		}
	}
}

func TestBuildPage_AskGateOff(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Hop: true, Visual: true})
	if strings.Contains(page, "ASK_ENABLED = true") {
		t.Errorf("ask-disabled page should not contain ASK_ENABLED = true")
	}
	if !strings.Contains(page, "ASK_ENABLED = false") {
		t.Errorf("expected ASK_ENABLED = false in page with ask off")
	}
}

func TestBuildPage_AskEnabled(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Hop: true, Visual: true, Ask: true})
	for _, want := range []string{"ASK_ENABLED = true", "mdpAskOpenInput", "mdpAskSubmit", `"select_ask":"c"`} {
		if !strings.Contains(page, want) {
			t.Errorf("ask-enabled page missing %q", want)
		}
	}
}

func TestBuildPage_AskRequiresVisual(t *testing.T) {
	// ask=true but visual=false: ASK_ENABLED must be false (no UI without
	// visual mode to host it).
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Hop: true, Ask: true})
	if strings.Contains(page, "ASK_ENABLED = true") {
		t.Errorf("ask without visual should resolve to ASK_ENABLED = false")
	}
}

func TestBuildPage_AskCardSizeDefaults(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Hop: true, Visual: true, Ask: true})
	for _, want := range []string{"ASK_CARD_WIDTH = 560", "ASK_CARD_HEIGHT = 50"} {
		if !strings.Contains(page, want) {
			t.Errorf("ask defaults page missing %q", want)
		}
	}
}

func TestBuildPage_AskCardSizeOverride(t *testing.T) {
	page := BuildPage(PageOptions{
		Body: "<p>x</p>", Theme: "dark", Hop: true, Visual: true, Ask: true,
		AskCardWidth: 720, AskCardHeight: 70,
	})
	for _, want := range []string{"ASK_CARD_WIDTH = 720", "ASK_CARD_HEIGHT = 70"} {
		if !strings.Contains(page, want) {
			t.Errorf("ask override page missing %q", want)
		}
	}
}

func TestBuildPage_AskHeaderBarBindings(t *testing.T) {
	page := BuildPage(PageOptions{Body: "<p>x</p>", Theme: "dark", Hop: true, Visual: true, Ask: true})
	for _, want := range []string{
		`"ask_again":"a"`,
		`mdpAskMakeKeyChip`,
		`mdpAskReask`,
		`isKey(e, 'close')`,
		`isKey(e, 'ask_again')`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("ask header bar / re-ask wiring missing %q", want)
		}
	}
}

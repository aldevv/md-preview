//go:build e2e

// End-to-end browser test for the client-side LaTeX rendering path.
// Spins up an httptest server with the production handler, drives a
// headless Chromium via playwright-go, and asserts pandoc.wasm
// initialized + latex-render.js swapped placeholders for rendered
// HTML + KaTeX picked up math markers, all with zero console errors.
//
// Gated behind the `e2e` build tag because it pulls in playwright-go
// and requires the Chromium browser to be installed:
//
//	go install github.com/playwright-community/playwright-go/cmd/playwright@latest
//	playwright install --with-deps chromium
//	make test-e2e
//
// CI doesn't run these by default; the build-tag keeps `go test ./...`
// hermetic. Run them locally before bumping pandoc.wasm or touching
// the latex/wasm asset bundle. The unit tests in server_test.go cover
// route/MIME/wiring correctness; this file covers what those can't:
// that the wasm actually instantiates and converts LaTeX in a real
// browser.
package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/playwright-community/playwright-go"
)

// pwOnce reuses a single Playwright driver + Chromium across all tests
// in this file. Each test launches its own browser context to keep
// state isolated; running the driver once amortizes the ~2 s startup.
var (
	pwOnce  sync.Once
	pwInst  *playwright.Playwright
	pwErr   error
	browser playwright.Browser
)

func sharedPlaywright(t *testing.T) (playwright.Browser, *playwright.Playwright) {
	t.Helper()
	pwOnce.Do(func() {
		pwInst, pwErr = playwright.Run()
		if pwErr != nil {
			return
		}
		headless := true
		browser, pwErr = pwInst.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
			Headless: &headless,
		})
	})
	if pwErr != nil {
		t.Skipf("playwright unavailable (run `playwright install chromium`): %v", pwErr)
	}
	return browser, pwInst
}

// startServerForFile writes content to a tempdir as <name>, primes
// state, and returns a running httptest server. Caller closes it.
func startServerForFile(t *testing.T, name, content string) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	s := newState(path, 0, "dark", false)
	s.doRender()
	srv := httptest.NewServer(newHandler(s))
	return srv, path
}

// newPageWithConsoleCapture opens a fresh context + page on the
// shared browser and records every error-level console message into
// the returned slice. Caller is responsible for closing the context.
func newPageWithConsoleCapture(t *testing.T, br playwright.Browser) (playwright.Page, playwright.BrowserContext, *[]string) {
	t.Helper()
	ctx, err := br.NewContext()
	if err != nil {
		t.Fatalf("new context: %v", err)
	}
	page, err := ctx.NewPage()
	if err != nil {
		_ = ctx.Close()
		t.Fatalf("new page: %v", err)
	}
	var mu sync.Mutex
	var errs []string
	page.OnConsole(func(m playwright.ConsoleMessage) {
		if m.Type() == "error" {
			mu.Lock()
			errs = append(errs, m.Text())
			mu.Unlock()
		}
	})
	return page, ctx, &errs
}

func TestE2E_TexFileRendersInBrowser(t *testing.T) {
	br, _ := sharedPlaywright(t)
	srv, _ := startServerForFile(t, "doc.tex", `\section{Hello}
This is \emph{LaTeX} with math $E = mc^2$.

\begin{itemize}
\item one
\item two
\end{itemize}
`)
	defer srv.Close()
	page, pctx, consoleErrors := newPageWithConsoleCapture(t, br)
	defer pctx.Close()

	if _, err := page.Goto(srv.URL + "/"); err != nil {
		t.Fatalf("goto: %v", err)
	}
	if _, err := page.WaitForSelector(".latex-block"); err != nil {
		t.Fatalf("latex-block never appeared (wasm init failed?): %v", err)
	}

	pending, _ := page.QuerySelectorAll(".latex-pending")
	blocks, _ := page.QuerySelectorAll(".latex-block")
	errs, _ := page.QuerySelectorAll(".latex-error")

	if len(pending) != 0 {
		t.Errorf("expected 0 .latex-pending after render, got %d", len(pending))
	}
	if len(blocks) != 1 {
		t.Errorf("expected 1 .latex-block, got %d", len(blocks))
	}
	if len(errs) != 0 {
		t.Errorf("expected 0 .latex-error, got %d", len(errs))
	}

	// h1 from pandoc's section -> html5 conversion.
	h1, _ := page.QuerySelector("#content h1")
	if h1 == nil {
		t.Errorf("rendered body missing <h1>")
	} else if txt, _ := h1.TextContent(); txt != "Hello" {
		t.Errorf("h1 text = %q, want %q", txt, "Hello")
	}

	// Inline math: pandoc emits \( \) which KaTeX picks up.
	katex, _ := page.QuerySelectorAll(".katex")
	if len(katex) < 1 {
		t.Errorf("expected at least 1 .katex span (KaTeX picked up math), got %d", len(katex))
	}

	if len(*consoleErrors) > 0 {
		t.Errorf("console errors during render: %v", *consoleErrors)
	}
}

func TestE2E_EmbeddedLatexFenceRenders(t *testing.T) {
	br, _ := sharedPlaywright(t)
	srv, _ := startServerForFile(t, "doc.md", `# Markdown heading

prose before

`+"```latex\n"+`\textbf{bold text} and equation $$\int_0^\infty e^{-x}dx = 1$$
`+"```"+`

prose after.
`)
	defer srv.Close()
	page, pctx, consoleErrors := newPageWithConsoleCapture(t, br)
	defer pctx.Close()

	if _, err := page.Goto(srv.URL + "/"); err != nil {
		t.Fatalf("goto: %v", err)
	}
	if _, err := page.WaitForSelector(".latex-block"); err != nil {
		t.Fatalf("latex-block never appeared: %v", err)
	}

	pending, _ := page.QuerySelectorAll(".latex-pending")
	blocks, _ := page.QuerySelectorAll(".latex-block")
	if len(pending) != 0 {
		t.Errorf("expected 0 pending, got %d", len(pending))
	}
	if len(blocks) != 1 {
		t.Errorf("expected 1 .latex-block, got %d", len(blocks))
	}

	// The surrounding markdown's <h1>Markdown heading</h1> must coexist
	// with the rendered fence (regression guard for goldmark passthrough).
	mdH1, _ := page.QuerySelector("#content > h1")
	if mdH1 == nil {
		t.Errorf("markdown <h1> missing; goldmark output got clobbered?")
	}

	// Bold from \textbf{} should be in the rendered block.
	strong, _ := page.QuerySelector(".latex-block strong")
	if strong == nil {
		t.Errorf("rendered block missing <strong> from \\textbf{}")
	}

	katex, _ := page.QuerySelectorAll(".katex")
	if len(katex) < 1 {
		t.Errorf("expected at least 1 .katex span for $$..$$ math, got %d", len(katex))
	}

	if len(*consoleErrors) > 0 {
		t.Errorf("console errors: %v", *consoleErrors)
	}
}

func TestE2E_NoLatexSkipsWasmBundle(t *testing.T) {
	// Math-free markdown must NOT pull in /_/pandoc.wasm or the glue
	// scripts. Catches a regression where hasMath() over-fires.
	br, _ := sharedPlaywright(t)
	srv, _ := startServerForFile(t, "doc.md", "# plain\n\njust text.\n")
	defer srv.Close()
	page, pctx, _ := newPageWithConsoleCapture(t, br)
	defer pctx.Close()

	var wasmFetched, latexJSFetched bool
	page.OnRequest(func(req playwright.Request) {
		switch {
		case strings.HasSuffix(req.URL(), "/_/pandoc.wasm"):
			wasmFetched = true
		case strings.HasSuffix(req.URL(), "/_/latex-render.js"):
			latexJSFetched = true
		}
	})

	if _, err := page.Goto(srv.URL + "/"); err != nil {
		t.Fatalf("goto: %v", err)
	}
	if _, err := page.WaitForSelector("#content h1"); err != nil {
		t.Fatalf("h1 never appeared: %v", err)
	}

	if wasmFetched {
		t.Errorf("/_/pandoc.wasm was fetched for math-free markdown (regression)")
	}
	if latexJSFetched {
		t.Errorf("/_/latex-render.js was fetched for math-free markdown (regression)")
	}
}

// TestMain runs all e2e tests, then tears down the shared browser.
// playwright.Stop() also shuts down the driver process.
func TestMain(m *testing.M) {
	code := m.Run()
	if browser != nil {
		_ = browser.Close()
	}
	if pwInst != nil {
		_ = pwInst.Stop()
	}
	os.Exit(code)
}

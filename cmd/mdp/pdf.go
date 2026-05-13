package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/aldevv/md-preview/internal/config"
	"github.com/aldevv/md-preview/internal/render"
	pdfpkg "github.com/aldevv/md-preview/internal/render/pdf"
)

const pdfUsage = `Usage: mdp pdf <file> [-o output.pdf] [-t dark|light]

Render a markdown file to PDF using headless Chrome/Chromium.
Output defaults to <file>.pdf in the same directory.
`

func runPDF(args []string, stdout, stderr io.Writer, env Environment) int {
	fs := flag.NewFlagSet("mdp pdf", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stdout, pdfUsage) }
	out := fs.String("o", "", "")
	themeLong := fs.String("theme", "", "")
	themeShort := fs.String("t", "", "")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}
	if fs.NArg() < 1 {
		fmt.Fprint(stdout, pdfUsage)
		return 1
	}
	src, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "mdp pdf: %v\n", err)
		return 1
	}
	info, err := env.Stat(src)
	if err != nil || info.IsDir() {
		fmt.Fprintf(stderr, "mdp pdf: file not found: %s\n", src)
		return 1
	}

	cfg, _ := env.LoadConfig()
	theme := pickFirst(*themeLong, *themeShort, cfg.Theme, "dark")
	if theme != "dark" && theme != "light" {
		fmt.Fprintf(stderr, "mdp pdf: invalid theme %q, using 'dark'\n", theme)
		theme = "dark"
	}

	outPath := *out
	if outPath == "" {
		outPath = strings.TrimSuffix(src, filepath.Ext(src)) + ".pdf"
	}
	if !filepath.IsAbs(outPath) {
		outPath, err = filepath.Abs(outPath)
		if err != nil {
			fmt.Fprintf(stderr, "mdp pdf: %v\n", err)
			return 1
		}
	}

	body, err := render.RenderBody(src)
	if err != nil {
		fmt.Fprintf(stderr, "mdp pdf: %v\n", err)
		// keep going: render.RenderBody returns an HTML <p>error</p>
		// body even on read failure, but if we couldn't even render
		// the body the user shouldn't get an empty PDF.
		return 1
	}
	page := render.BuildPage(body, theme, 0, config.ExtraCSS(cfg, stderr), cfg.Colemak)
	tmpPath := tmpHTMLPath(env.TempDir(), src)
	if err := writeTmpFile(tmpPath, []byte(page)); err != nil {
		fmt.Fprintf(stderr, "mdp pdf: writing tmp html: %v\n", err)
		return 1
	}

	chromium := config.ChromiumPath(env.LookPath, env.GOOS)
	if chromium == "" {
		fmt.Fprintln(stderr, "mdp pdf: Chrome/Chromium not found on PATH. "+
			"Install via `apt install chromium`, `brew install --cask chromium`, "+
			"or visit https://www.google.com/chrome.")
		return 1
	}

	if err := pdfpkg.Render(tmpPath, outPath, chromium, env.RunCmd); err != nil {
		fmt.Fprintf(stderr, "mdp pdf: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, outPath)
	return 0
}

// pickFirst returns the first non-empty value, or "" if all empty.
func pickFirst(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

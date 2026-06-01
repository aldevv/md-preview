// Command mdp renders a markdown file to HTML and opens it in a browser.
//
// One-shot static preview: writes HTML to a stable temp file (sha1 of the
// input path so re-runs overwrite) and launches a browser. The `mdp serve`
// subcommand starts the long-running preview server consumed by the
// Neovim plugin.
package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"fmt"
	htmlpkg "html"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/aldevv/md-preview/internal/config"
	"github.com/aldevv/md-preview/internal/nativewin"
	"github.com/aldevv/md-preview/internal/osutil"
	"github.com/aldevv/md-preview/internal/render"
	"github.com/aldevv/md-preview/internal/render/pandoc"
	"github.com/aldevv/md-preview/internal/server"
)

// Environment is the seam between run() and the OS. Production wires it
// up via realEnv, tests substitute fakes.
type Environment struct {
	LookPath func(string) (string, error)
	GOOS     string
	GOARCH   string
	Stat     func(string) (os.FileInfo, error)
	TempDir  func() string
	Getwd    func() (string, error)
	// FzfPick returns "" on cancellation; a non-nil error only when fzf
	// itself is unavailable.
	FzfPick func(ctx context.Context, cwd string) (string, error)
	// LoadConfig returns a non-nil error only on parse failure; missing
	// file is not an error.
	LoadConfig func() (config.Config, error)
	Spawn      func(argv []string) error
	Exec       func(path string, argv []string, env []string) error
	// RunServer starts the preview server and blocks. Stubbed in tests so
	// the -w/--watch path stays hermetic.
	RunServer  func(server.Options) error
	Executable func() (string, error)
	// HTTPGet returns the response body for 2xx, error otherwise. Caller closes.
	HTTPGet func(url string) (io.ReadCloser, error)
	// RunCmd runs synchronously with stdout/stderr inherited. If environ is
	// nil, the parent environment is used as-is.
	RunCmd func(name string, args []string, environ []string) error
	// OpenWindow opens a native preview window at url and blocks until
	// the user closes it. Returns nativewin.ErrUnsupported when the
	// platform has no backend or required runtime libraries are missing.
	// Callers should fall back to Spawn on ErrUnsupported.
	OpenWindow func(url string) error
	// StartWindowChild spawns the current binary as a detached
	// `mdp __window -` child with stdin connected to the returned
	// writer. The caller writes the URL (one line) when rendering is
	// done, then closes the writer; the child blocks reading the URL
	// in parallel with the parent's render. Returns an error when
	// native is unavailable or the spawn fails; callers fall back to
	// the synchronous OpenWindow / browser path.
	StartWindowChild func() (io.WriteCloser, error)
}

func realEnv() Environment {
	return Environment{
		LookPath:         exec.LookPath,
		GOOS:             runtime.GOOS,
		GOARCH:           runtime.GOARCH,
		Stat:             os.Stat,
		TempDir:          os.TempDir,
		Getwd:            func() (string, error) { return os.Getwd() },
		FzfPick:          config.FzfPick,
		LoadConfig:       config.Load,
		Spawn:            spawnDetached,
		Exec:             osutil.ReplaceProcess,
		RunServer:        server.Run,
		Executable:       os.Executable,
		HTTPGet:          httpGet,
		RunCmd:           runCmdInherit,
		OpenWindow:       openNativeWindow,
		StartWindowChild: startWindowChildDetached,
	}
}

// startWindowChildDetached spawns the current binary as `mdp __window -`
// with stdin piped, Setsid for detachment. The caller writes the URL
// later. Returns ErrUnsupported when nativewin isn't loadable so the
// caller can drop straight to the browser path.
//
// In MDP_DEBUG=1 the child's stderr is appended to
// $MDP_DEBUG_LOG (default /tmp/mdp-debug.log) so the [mdp-time]
// markers survive Setsid detachment; tail that file to watch the
// child boot.
func startWindowChildDetached() (io.WriteCloser, error) {
	if !nativewin.Available() {
		return nil, nativewin.ErrUnsupported
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "__window", "-")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if os.Getenv("MDP_DEBUG") == "1" {
		path := os.Getenv("MDP_DEBUG_LOG")
		if path == "" {
			path = "/tmp/mdp-debug.log"
		}
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			_, _ = fmt.Fprintf(f, "\n=== mdp child %s ===\n", time.Now().Format(time.RFC3339Nano))
			cmd.Stderr = f
			cmd.Stdout = f
		}
	}
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	cmd.SysProcAttr = osutil.DetachAttr()
	if err := cmd.Start(); err != nil {
		_ = stdinPipe.Close()
		return nil, err
	}
	return stdinPipe, nil
}

// Probes Available() lazily; the Linux probe dlopens libgtk/libwebkit,
// so users who never opt into the native path don't pay that cost.
func openNativeWindow(url string) error {
	if !nativewin.Available() {
		return nativewin.ErrUnsupported
	}
	return nativewin.Open(nativewin.Options{URL: url, Title: "mdp"})
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, realEnv()))
}

const usage = `Usage: mdp [flags] [file]

Render a markdown file in a chromeless native window (or browser).

If no file is given, mdp uses fzf to pick one interactively. fzf must be
on PATH for the picker, pass a file argument otherwise.

By default mdp opens a native GTK/WebKit (Linux) or Cocoa (macOS) window.
Set MDP_NATIVE=0 to force the browser path instead. If the native window
runtime is missing, mdp falls back to a chromium-family browser; if none
is installed, the first available browser is used and a one-time banner
suggests installing one for independent windows.

Flags:
  -e, --edit       Also open the file in nvim after launching the preview
      --no-edit    Override config to skip opening nvim
  -t, --theme      Theme: "dark" or "light" (default: from config or "dark")
  -p, --print      Print HTML path instead of opening a browser
  -h, --help       Show this help

Subcommands:
  mdp help                          Show this help
  mdp version                       Print the installed version and exit
  mdp watch [-t theme] [file]       Open the preview and auto-refresh when
                                    the file changes (any editor). Stays
                                    running until you Ctrl-C.
  mdp update [--check] [--force]    Update mdp to the latest GitHub release.
             [--version vX.Y.Z]     Pin a specific tag with --version.
                                    --check only reports whether one is
                                    available without installing.
  mdp skill path                    Print the path to the bundled skill
                                    reference (for Claude Code skills and
                                    other automation driving mdp).
  mdp serve <file> <port> <theme>   Start the preview server (used by the
                                    md-preview.nvim Neovim plugin). Run
                                    directly in a TTY to also get a
                                    native window.
  mdp pdf <file> [-o output.pdf]    Render the markdown file to PDF
                                    using headless Chrome/Chromium.
`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, env Environment) int {
	if err := config.EnsureDefault(); err != nil {
		fmt.Fprintf(stderr, "mdp: seeding default config: %v\n", err)
	}

	if len(args) > 0 {
		switch args[0] {
		case "serve":
			return runServe(args[1:], stdin, stderr, env)
		case "watch":
			return runWatchSubcommand(args[1:], stdout, stderr, env)
		case "pdf":
			return runPDF(args[1:], stdout, stderr, env)
		case "skill":
			return runSkill(args[1:], stdout, stderr, env)
		case "update":
			return runUpdate(args[1:], stdout, stderr, env)
		case "__window":
			return runWindow(args[1:], stderr, env)
		case "__sidecar":
			return runSidecar(args[1:], stderr)
		case "help":
			fmt.Fprint(stdout, usage)
			return 0
		case "version", "--version", "-v":
			fmt.Fprintln(stdout, buildVersion())
			return 0
		}
	}

	pruneStaleTmpFiles(env.TempDir(), stderr)

	flags, code, done := parseRunFlags(args, stdout, stderr)
	if done {
		return code
	}

	rc, code, done := resolveAndValidate(flags.positional, flags.theme, env, stderr, func() int {
		fmt.Fprint(stdout, usage)
		return 0
	})
	if done {
		return code
	}

	parentMark := newParentTimer()
	parentMark("after parseRunFlags + resolveAndValidate")

	// Check if a chromium-family browser is already running. If so,
	// it's cheaper to open --app= in the warm browser (~50-100ms) than
	// to cold-start the native WebKit window (~800-900ms). Honors
	// PreferRunningBrowser config + MDP_NO_BROWSER_REUSE env override,
	// and only kicks in for the auto browser default (a user-pinned
	// browser config is respected as-is).
	runningChromium := ""
	if !flags.printPath && wantPreferRunningBrowser(rc.cfg) && isAutoBrowser(rc.cfg.Browser) {
		runningChromium = config.RunningChromiumBin(env.LookPath, env.GOOS)
		if runningChromium != "" {
			parentMark("found running chromium: " + runningChromium)
		}
	}

	// Pre-spawn the native-window child (if we're going that route) so
	// its Go runtime + GTK + WebKit init overlaps with our markdown
	// render. We send the URL down stdin once the render finishes.
	// On abort (render error, -p print mode), close the pipe and the
	// child exits silently on EOF. Skipped entirely when we already
	// know a warm chromium browser is going to handle the preview.
	var childStdin io.WriteCloser
	if runningChromium == "" && !flags.printPath && !isPDFPath(rc.src) && wantNative() && env.StartWindowChild != nil {
		if pipe, err := env.StartWindowChild(); err == nil {
			childStdin = pipe
			parentMark("StartWindowChild spawned")
		} else if err != nativewin.ErrUnsupported {
			fmt.Fprintf(stderr, "mdp: window child unavailable (%v); falling back to inline/browser\n", err)
		}
	}

	// Promote-on-demand sidecar: a tiny background process the static
	// page can navigate to (AI icon click, 'c' key) to upgrade itself
	// into a live watch session. The spawn is fire-and-forget on a
	// deterministic per-file port so the parent never waits on it.
	if !flags.printPath && rc.cfg.Ask && !isPDFPath(rc.src) && render.IsWalkableExt(rc.src) {
		rc.sidecarURL = startSidecar(rc, env, stderr)
		if rc.sidecarURL != "" {
			parentMark("sidecar dispatched to " + rc.sidecarURL)
		}
	}

	tmpPath, ok := renderEntry(rc, env, stderr)
	parentMark("renderEntry done")
	if !ok {
		if childStdin != nil {
			_ = childStdin.Close()
		}
		return 1
	}

	if flags.printPath {
		if childStdin != nil {
			_ = childStdin.Close()
		}
		fmt.Fprintln(stdout, tmpPath)
		return 0
	}

	url := "file://" + tmpPath
	skipNative := isPDFPath(rc.src)
	switch {
	case runningChromium != "":
		argv := []string{runningChromium, "--app=" + url}
		if err := env.Spawn(argv); err != nil {
			fmt.Fprintf(stderr, "mdp: launching running browser: %v\n", err)
			if !openPreview(url, rc.cfg, env, stderr, skipNative) {
				return 1
			}
		}
		parentMark("running chromium spawned")
	case childStdin != nil:
		_, werr := fmt.Fprintln(childStdin, url)
		_ = childStdin.Close()
		parentMark("URL written to child stdin")
		if werr != nil {
			fmt.Fprintf(stderr, "mdp: sending URL to window child: %v\n", werr)
			if !openPreview(url, rc.cfg, env, stderr, skipNative) {
				return 1
			}
		}
	default:
		if !openPreview(url, rc.cfg, env, stderr, skipNative) {
			return 1
		}
	}

	if !flags.editEnabled(rc.cfg) {
		return 0
	}
	return maybeOpenEditor(rc.src, env, stderr)
}

// openPreview is the fallback path used when the parallel
// StartWindowChild route in run() couldn't be taken (native disabled,
// runtime unavailable, or spawn failed). It tries inline OpenWindow
// (blocks until the window closes), then falls back to a browser
// spawn with the #mdp-install-chrome hash appended for non-chromium
// fallback launches. skipNative bypasses the WebKit window entirely
// (PDFs are routed straight to the browser since WebKit can't render
// embedded application/pdf content).
func openPreview(url string, cfg config.Config, env Environment, stderr io.Writer, skipNative bool) bool {
	if !skipNative && wantNative() && env.OpenWindow != nil {
		if err := env.OpenWindow(url); err == nil {
			return true
		} else if err != nativewin.ErrUnsupported {
			fmt.Fprintf(stderr, "mdp: native window unavailable (%v); falling back to browser\n", err)
		}
	}
	argv := config.BrowserCmd(cfg.Browser, url, env.LookPath, env.GOOS, stderr)
	if !config.IsChromiumApp(argv) {
		argv = withInstallHash(argv)
	}
	if err := env.Spawn(argv); err != nil {
		fmt.Fprintf(stderr, "mdp: launching browser: %v\n", err)
		return false
	}
	return true
}

// runWindow is the hidden subcommand the parent invokes (detached) to
// hold the native window. Pass "-" to read the URL from stdin (used by
// the parallel-render path so the child can start WebKit init while
// the parent finishes rendering). Not documented in usage by design.
func runWindow(args []string, stderr io.Writer, env Environment) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "Usage: mdp __window <url|->")
		return 1
	}
	url := args[0]
	if url == "-" {
		sc := bufio.NewScanner(os.Stdin)
		if !sc.Scan() {
			fmt.Fprintln(stderr, "mdp __window: no URL on stdin")
			return 1
		}
		url = strings.TrimSpace(sc.Text())
		if url == "" {
			fmt.Fprintln(stderr, "mdp __window: empty URL")
			return 1
		}
	}
	if env.OpenWindow == nil {
		fmt.Fprintln(stderr, "mdp __window: native window not supported on this build")
		return 1
	}
	if err := env.OpenWindow(url); err != nil {
		fmt.Fprintf(stderr, "mdp __window: %v\n", err)
		return 1
	}
	return 0
}

// withInstallHash returns a copy of argv with #mdp-install-chrome
// appended to the URL (the last element of argv per BrowserCmd's
// convention).
func withInstallHash(argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	out := make([]string, len(argv))
	copy(out, argv)
	last := len(out) - 1
	if !strings.Contains(out[last], "#") {
		out[last] = out[last] + "#mdp-install-chrome"
	}
	return out
}

// newParentTimer returns a marker function that prints elapsed time
// from now whenever MDP_DEBUG=1 is set. Matches the [mdp-time] prefix
// used by the nativewin backend so timings line up. No-op otherwise.
func newParentTimer() func(label string) {
	if os.Getenv("MDP_DEBUG") != "1" {
		return func(string) {}
	}
	t0 := time.Now()
	return func(label string) {
		fmt.Fprintf(os.Stderr, "[mdp-time] parent: %-34s %v\n", label, time.Since(t0))
	}
}

// wantNative returns false only when MDP_NATIVE is explicitly disabled
// (0/false). The native window is the default; the env var is the
// opt-out.
func wantNative() bool {
	v := os.Getenv("MDP_NATIVE")
	return v != "0" && v != "false"
}

// wantPreferRunningBrowser is the "if a chromium browser is already
// running, send the preview there" toggle. Defaults to true. The env
// var MDP_NO_BROWSER_REUSE=1 forces false; the config flag overrides
// the default when set.
func wantPreferRunningBrowser(cfg config.Config) bool {
	if v := os.Getenv("MDP_NO_BROWSER_REUSE"); v == "1" || v == "true" {
		return false
	}
	if cfg.PreferRunningBrowser != nil {
		return *cfg.PreferRunningBrowser
	}
	return true
}

// isAutoBrowser reports whether cfg.Browser is unset / "auto", i.e. we
// own the browser-choice decision. When the user has pinned a specific
// browser we honor it as-is and skip the running-chromium shortcut.
func isAutoBrowser(b any) bool {
	switch v := b.(type) {
	case nil:
		return true
	case string:
		return v == "" || v == "auto"
	}
	return false
}

type runFlags struct {
	positional string
	theme      string
	printPath  bool
	editSet    bool
	noEditSet  bool
	editOn     bool
}

func (f runFlags) editEnabled(cfg config.Config) bool {
	switch {
	case f.editSet:
		return f.editOn
	case f.noEditSet:
		return false
	default:
		return cfg.Edit
	}
}

func parseRunFlags(args []string, stdout, stderr io.Writer) (flags runFlags, exitCode int, done bool) {
	fs := flag.NewFlagSet("mdp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stdout, usage) }

	var (
		editLong   = fs.Bool("edit", false, "")
		editShort  = fs.Bool("e", false, "")
		_          = fs.Bool("no-edit", false, "")
		themeLong  = fs.String("theme", "", "")
		themeShort = fs.String("t", "", "")
		printLong  = fs.Bool("print", false, "")
		printShort = fs.Bool("p", false, "")
	)

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return runFlags{}, 0, true
		}
		return runFlags{}, 1, true
	}

	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "edit", "e":
			flags.editSet = true
		case "no-edit":
			flags.noEditSet = true
		}
	})

	if flags.editSet && flags.noEditSet {
		fmt.Fprintln(stderr, "mdp: -e/--edit and --no-edit conflict")
		return runFlags{}, 1, true
	}

	flags.editOn = *editLong || *editShort
	flags.printPath = *printLong || *printShort
	flags.theme = *themeLong
	if flags.theme == "" {
		flags.theme = *themeShort
	}
	if fs.NArg() > 0 {
		flags.positional = fs.Arg(0)
	}
	return flags, 0, false
}

type resolved struct {
	src        string
	theme      string
	cfg        config.Config
	sidecarURL string
}

// onFzfMissing fires when no positional was given and fzf is unavailable;
// its return value becomes the exit code. run() shows usage and exits 0,
// watch surfaces an error and exits 1.
func resolveAndValidate(positional, themeFlag string, env Environment, stderr io.Writer, onFzfMissing func() int) (rc resolved, exitCode int, done bool) {
	cfg, err := env.LoadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "mdp: config: %v\n", err)
	}
	rc.cfg = cfg

	file := positional
	if file == "" {
		cwd, err := env.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "mdp: %v\n", err)
			return rc, 1, true
		}
		pick, err := env.FzfPick(context.Background(), cwd)
		if err != nil {
			return rc, onFzfMissing(), true
		}
		if pick == "" {
			return rc, 0, true
		}
		file = pick
	}

	src, err := filepath.Abs(file)
	if err != nil {
		fmt.Fprintf(stderr, "mdp: %v\n", err)
		return rc, 1, true
	}
	info, err := env.Stat(src)
	if err != nil || info.IsDir() {
		fmt.Fprintf(stderr, "mdp: file not found: %s\n", src)
		return rc, 1, true
	}
	rc.src = src

	theme := themeFlag
	if theme == "" {
		theme = cfg.Theme
	}
	if theme == "" {
		theme = "dark"
	}
	if theme != "dark" && theme != "light" {
		fmt.Fprintf(stderr, "mdp: invalid theme %q, using 'dark'\n", theme)
		theme = "dark"
	}
	rc.theme = theme

	if format := pandoc.InputFormat(src); format != "" {
		if _, err := pandoc.Ensure(context.Background(), format, stderr); err != nil {
			fmt.Fprintf(stderr, "mdp: %v\n", err)
			return rc, 1, true
		}
	}
	return rc, 0, false
}

func renderEntry(rc resolved, env Environment, stderr io.Writer) (string, bool) {
	if isPDFPath(rc.src) {
		path, err := renderPDFWrapper(rc.src, rc.theme, env.TempDir())
		if err != nil {
			fmt.Fprintf(stderr, "mdp: %v\n", err)
			return "", false
		}
		return path, true
	}
	if render.IsWalkableExt(rc.src) {
		return renderEntryAsStaticTree(rc, env, stderr)
	}
	return renderEntryAsSingleFile(rc, env, stderr)
}

func isPDFPath(p string) bool {
	return strings.EqualFold(filepath.Ext(p), ".pdf")
}

// renderPDFWrapper writes a chromeless HTML shell that embeds the PDF so
// chrome's --app= mode (no toolbar, no URL bar) hosts the built-in PDF
// viewer for it. Returns the path to the wrapper HTML.
func renderPDFWrapper(pdfPath, theme, tmpDir string) (string, error) {
	bg := "#1e1e1e"
	if theme == "light" {
		bg = "#ffffff"
	}
	pdfURL := (&url.URL{Scheme: "file", Path: pdfPath}).String()
	html := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>%s</title>
<style>html,body{margin:0;padding:0;height:100%%;background:%s;overflow:hidden}
embed{display:block;width:100vw;height:100vh;border:0}</style>
</head><body><embed src="%s" type="application/pdf"></body></html>`,
		htmlpkg.EscapeString(filepath.Base(pdfPath)), bg, htmlpkg.EscapeString(pdfURL))
	out := tmpHTMLPath(tmpDir, pdfPath)
	if err := os.WriteFile(out, []byte(html), 0o644); err != nil {
		return "", err
	}
	return out, nil
}

func renderEntryAsStaticTree(rc resolved, env Environment, stderr io.Writer) (string, bool) {
	opts := render.StaticTreeOptions{
		Theme:       rc.theme,
		ExtraCSS:    config.ExtraCSS(rc.cfg, stderr),
		Colemak:     rc.cfg.Colemak,
		FileTree:    rc.cfg.FileTree,
		FuzzyFinder: rc.cfg.FuzzyFinder,
		Hop:         rc.cfg.Hop,
		Visual:      rc.cfg.Visual,
		Ask:         rc.cfg.Ask,
		Keys:        rc.cfg.Keys,
		SidecarURL:  rc.sidecarURL,
	}
	entryHTML, err := render.RenderStaticTree(rc.src, env.TempDir(), opts)
	if err != nil {
		fmt.Fprintf(stderr, "mdp: %v\n", err)
		return "", false
	}
	return entryHTML, true
}

func renderEntryAsSingleFile(rc resolved, env Environment, stderr io.Writer) (string, bool) {
	body, err := render.RenderBody(rc.src)
	if err != nil {
		fmt.Fprintf(stderr, "mdp: %v\n", err)
		return "", false
	}
	baseDir := filepath.Dir(rc.src)
	body = render.RewriteImgSrc(body, baseDir, func(abs string) (string, bool) {
		rel, err := filepath.Rel(baseDir, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", false
		}
		return render.FileURL(abs), true
	})
	page := render.BuildPageWithKeys(body, rc.theme, 0, config.ExtraCSS(rc.cfg, stderr), rc.cfg.Colemak, rc.src, false, false, "", rc.cfg.Hop, rc.cfg.Visual, rc.cfg.Ask, rc.cfg.Keys)
	page = render.InjectSidecarURL(page, rc.sidecarURL)
	tmpPath := tmpHTMLPath(env.TempDir(), rc.src)
	if err := writeTmpFile(tmpPath, []byte(page)); err != nil {
		fmt.Fprintf(stderr, "mdp: writing tmp: %v\n", err)
		return "", false
	}
	return tmpPath, true
}

func maybeOpenEditor(src string, env Environment, stderr io.Writer) int {
	editor := ""
	for _, c := range []string{"nvim", "vim"} {
		if p, err := env.LookPath(c); err == nil && p != "" {
			editor = p
			break
		}
	}
	if editor == "" {
		fmt.Fprintln(stderr, "mdp: nvim/vim not found on PATH; preview opened, edit skipped.")
		return 0
	}
	if err := env.Exec(editor, []string{filepath.Base(editor), src}, os.Environ()); err != nil {
		fmt.Fprintf(stderr, "mdp: exec %s: %v\n", editor, err)
		return 1
	}
	return 0
}

// Stable per-source path so re-runs overwrite rather than accumulate.
func tmpHTMLPath(tmpdir, src string) string {
	sum := sha1.Sum([]byte(src))
	digest := hex.EncodeToString(sum[:])[:12]
	return filepath.Join(tmpdir, "mdp-"+digest+".html")
}

const tmpFileTTL = 7 * 24 * time.Hour

// pruneStaleTmpFiles removes mdp-*.html entries in tmpdir whose mtime is
// older than tmpFileTTL. Failures are non-fatal (best-effort GC); a
// single warning on read goes to stderr so a wedged tmpdir is visible.
func pruneStaleTmpFiles(tmpdir string, stderr io.Writer) {
	entries, err := os.ReadDir(tmpdir)
	if err != nil {
		fmt.Fprintf(stderr, "mdp: pruning %s: %v\n", tmpdir, err)
		return
	}
	cutoff := time.Now().Add(-tmpFileTTL)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "mdp-") || !strings.HasSuffix(name, ".html") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(tmpdir, name))
		}
	}
}

// O_NOFOLLOW: the stable filename in a shared /tmp is otherwise vulnerable
// to a foreign-user-planted symlink redirecting our truncate to e.g.
// ~/.bashrc; ELOOP makes the open fail cleanly in that case.
func writeTmpFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|osutil.ONoFollow, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// Injected via `-ldflags -X main.version=...` for release builds and
// `make install`. Empty for module-mode `go install`, in which case
// buildVersion falls back to debug.ReadBuildInfo.
var version string

func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "(unknown)"
	}
	return info.Main.Version
}

// Setsid so closing the terminal doesn't kill the browser child.
func spawnDetached(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty browser command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = osutil.DetachAttr()
	return cmd.Start()
}

func runWatchSubcommand(args []string, stdout, stderr io.Writer, env Environment) int {
	fs := flag.NewFlagSet("mdp watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stdout, "Usage: mdp watch [-t dark|light] [file]")
	}
	themeLong := fs.String("theme", "", "")
	themeShort := fs.String("t", "", "")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}

	theme := *themeLong
	if theme == "" {
		theme = *themeShort
	}
	positional := ""
	if fs.NArg() > 0 {
		positional = fs.Arg(0)
	}

	rc, code, done := resolveAndValidate(positional, theme, env, stderr, func() int {
		fmt.Fprintln(stderr, "mdp watch: pass a file or install fzf for the picker")
		return 1
	})
	if done {
		return code
	}

	opts := server.Options{
		File:          rc.src,
		Port:          0,
		Theme:         rc.theme,
		Colemak:       rc.cfg.Colemak,
		FileTree:      rc.cfg.FileTree,
		FuzzyFinder:   rc.cfg.FuzzyFinder,
		Hop:           rc.cfg.Hop,
		Visual:        rc.cfg.Visual,
		Ask:           rc.cfg.Ask,
		AskCommand:    rc.cfg.AskCommand,
		AskTimeoutSec: rc.cfg.AskTimeoutSec,
		Keys:          rc.cfg.Keys,
		Watch:         true,
		ExtraCSS:      config.ExtraCSS(rc.cfg, stderr),
	}

	if wantNative() && env.OpenWindow != nil {
		return runWatchWithNativeWindow(opts, rc.cfg, env, stderr)
	}
	return runWatchWithBrowser(opts, rc.cfg, env, stderr)
}

func runWatchWithBrowser(opts server.Options, cfg config.Config, env Environment, stderr io.Writer) int {
	opts.OnListen = func(port int) {
		url := fmt.Sprintf("http://localhost:%d/", port)
		argv := config.BrowserCmd(cfg.Browser, url, env.LookPath, env.GOOS, stderr)
		if !config.IsChromiumApp(argv) {
			argv = withInstallHash(argv)
		}
		if err := env.Spawn(argv); err != nil {
			fmt.Fprintf(stderr, "mdp: launching browser: %v\n", err)
		}
	}
	if err := env.RunServer(opts); err != nil {
		fmt.Fprintf(stderr, "mdp: %v\n", err)
		return 1
	}
	return 0
}

// Cocoa requires the NSApp run loop on the OS main thread; main_darwin.go
// locks the main goroutine for that reason, so the native window must
// block here while the server runs in a goroutine.
func runWatchWithNativeWindow(opts server.Options, cfg config.Config, env Environment, stderr io.Writer) int {
	listenCh := make(chan int, 1)
	opts.OnListen = func(port int) { listenCh <- port }
	serverDone := make(chan error, 1)
	go func() { serverDone <- env.RunServer(opts) }()

	select {
	case port := <-listenCh:
		url := fmt.Sprintf("http://localhost:%d/", port)
		if err := env.OpenWindow(url); err != nil {
			fmt.Fprintf(stderr, "mdp: native window unavailable (%v); falling back to browser\n", err)
			argv := config.BrowserCmd(cfg.Browser, url, env.LookPath, env.GOOS, stderr)
			if !config.IsChromiumApp(argv) {
				argv = withInstallHash(argv)
			}
			if e2 := env.Spawn(argv); e2 != nil {
				fmt.Fprintf(stderr, "mdp: launching browser: %v\n", e2)
				return 1
			}
			if err := <-serverDone; err != nil {
				fmt.Fprintf(stderr, "mdp: %v\n", err)
				return 1
			}
			return 0
		}
		// Window closed cleanly. Drain serverDone non-blocking so a
		// server-side crash during the session doesn't silently exit 0.
		select {
		case err := <-serverDone:
			if err != nil {
				fmt.Fprintf(stderr, "mdp: %v\n", err)
				return 1
			}
		default:
			// Server still running; process exit will clean it up.
		}
		return 0
	case err := <-serverDone:
		if err != nil {
			fmt.Fprintf(stderr, "mdp: %v\n", err)
			return 1
		}
		return 0
	}
}

// MDP_COLEMAK=1 in the environment overrides config.toml's colemak flag.
func runServe(args []string, stdin io.Reader, stderr io.Writer, env Environment) int {
	if len(args) < 3 {
		fmt.Fprintln(stderr, "Usage: mdp serve <file> <port> <theme>")
		return 1
	}
	port, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "mdp serve: invalid port %q\n", args[1])
		return 1
	}
	cfg, _ := config.Load()
	colemak := cfg.Colemak
	if v := os.Getenv("MDP_COLEMAK"); v == "1" || v == "true" {
		colemak = true
	}
	if format := pandoc.InputFormat(args[0]); format != "" {
		// pandoc.Ensure writes install-progress lines to its writer; the
		// nvim plugin's on_stderr surfaces those as red error toasts, so
		// silence them in serve mode. run()/runWatchSubcommand keep stderr.
		if _, err := pandoc.Ensure(context.Background(), format, io.Discard); err != nil {
			fmt.Fprintf(stderr, "mdp serve: %v\n", err)
			return 1
		}
	}
	opts := server.Options{
		File:          args[0],
		Port:          port,
		Theme:         args[2],
		Colemak:       colemak,
		FileTree:      cfg.FileTree,
		FuzzyFinder:   cfg.FuzzyFinder,
		Hop:           cfg.Hop,
		Visual:        cfg.Visual,
		Ask:           cfg.Ask,
		AskCommand:    cfg.AskCommand,
		AskTimeoutSec: cfg.AskTimeoutSec,
		Keys:          cfg.Keys,
		ExtraCSS:      config.ExtraCSS(cfg, stderr),
	}
	// Direct-shell invocations (stdin is a TTY) get the native window
	// like plain mdp/watch. The nvim plugin spawns mdp serve with a
	// pipe on stdin and opens its own browser, so we leave that path
	// untouched.
	if stdinIsTTY(stdin) && wantNative() && env.OpenWindow != nil {
		return runServeWithNativeWindow(opts, cfg, env, stderr)
	}
	if err := server.Run(opts); err != nil {
		fmt.Fprintf(stderr, "mdp serve: %v\n", err)
		return 1
	}
	return 0
}

func stdinIsTTY(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func runServeWithNativeWindow(opts server.Options, cfg config.Config, env Environment, stderr io.Writer) int {
	listenCh := make(chan int, 1)
	prevOnListen := opts.OnListen
	opts.OnListen = func(port int) {
		if prevOnListen != nil {
			prevOnListen(port)
		}
		listenCh <- port
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- env.RunServer(opts) }()

	select {
	case port := <-listenCh:
		url := fmt.Sprintf("http://localhost:%d/", port)
		if err := env.OpenWindow(url); err != nil {
			fmt.Fprintf(stderr, "mdp: native window unavailable (%v); falling back to browser\n", err)
			argv := config.BrowserCmd(cfg.Browser, url, env.LookPath, env.GOOS, stderr)
			if !config.IsChromiumApp(argv) {
				argv = withInstallHash(argv)
			}
			if e2 := env.Spawn(argv); e2 != nil {
				fmt.Fprintf(stderr, "mdp: launching browser: %v\n", e2)
				return 1
			}
			if err := <-serverDone; err != nil {
				fmt.Fprintf(stderr, "mdp serve: %v\n", err)
				return 1
			}
			return 0
		}
		select {
		case err := <-serverDone:
			if err != nil {
				fmt.Fprintf(stderr, "mdp serve: %v\n", err)
				return 1
			}
		default:
		}
		return 0
	case err := <-serverDone:
		if err != nil {
			fmt.Fprintf(stderr, "mdp serve: %v\n", err)
			return 1
		}
		return 0
	}
}

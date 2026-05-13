// Command mdp renders a markdown file to HTML and opens it in a browser.
//
// One-shot static preview: writes HTML to a stable temp file (sha1 of the
// input path so re-runs overwrite) and launches a browser. The `mdp serve`
// subcommand starts the long-running preview server consumed by the
// Neovim plugin.
package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aldevv/md-preview/internal/config"
	"github.com/aldevv/md-preview/internal/nativewin"
	"github.com/aldevv/md-preview/internal/render"
	"github.com/aldevv/md-preview/internal/render/latex"
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
}

func realEnv() Environment {
	return Environment{
		LookPath:   exec.LookPath,
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		Stat:       os.Stat,
		TempDir:    os.TempDir,
		Getwd:      func() (string, error) { return os.Getwd() },
		FzfPick:    config.FzfPick,
		LoadConfig: config.Load,
		Spawn:      spawnDetached,
		Exec:       syscall.Exec,
		RunServer:  server.Run,
		Executable: os.Executable,
		HTTPGet:    httpGet,
		RunCmd:     runCmdInherit,
		OpenWindow: openNativeWindow,
	}
}

// openNativeWindow is the production wiring for Environment.OpenWindow.
// Probes Available() lazily (the Linux probe dlopens libgtk/libwebkit),
// so users who never opt into the native path don't pay for it.
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

Render a markdown file in a browser.

If no file is given, mdp uses fzf to pick one interactively. fzf must be
on PATH for the picker — pass a file argument otherwise.

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
                                    md-preview.nvim Neovim plugin).
`

func run(args []string, _ io.Reader, stdout, stderr io.Writer, env Environment) int {
	// EnsureDefault is idempotent; failure is non-fatal (mdp works without a config).
	if err := config.EnsureDefault(); err != nil {
		fmt.Fprintf(stderr, "mdp: seeding default config: %v\n", err)
	}

	if len(args) > 0 {
		switch args[0] {
		case "serve":
			return runServe(args[1:], stderr)
		case "watch":
			return runWatchSubcommand(args[1:], stdout, stderr, env)
		case "skill":
			return runSkill(args[1:], stdout, stderr, env)
		case "update":
			return runUpdate(args[1:], stdout, stderr, env)
		case "_open-window":
			return runOpenWindow(args[1:], stderr, env)
		case "help":
			fmt.Fprint(stdout, usage)
			return 0
		case "version", "--version", "-v":
			fmt.Fprintln(stdout, buildVersion())
			return 0
		}
	}

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
		// flag.ContinueOnError returns flag.ErrHelp when -h/--help is
		// passed; fs.Usage already printed the help text, so just exit 0.
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}

	editSet, noEditSet := false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "edit", "e":
			editSet = true
		case "no-edit":
			noEditSet = true
		}
	})

	if editSet && noEditSet {
		fmt.Fprintln(stderr, "mdp: -e/--edit and --no-edit conflict")
		return 1
	}

	editFlagOn := *editLong || *editShort
	printPath := *printLong || *printShort

	theme := *themeLong
	if theme == "" {
		theme = *themeShort
	}

	cfg, err := env.LoadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "mdp: config: %v\n", err)
	}

	var edit bool
	switch {
	case editSet:
		edit = editFlagOn
	case noEditSet:
		edit = false
	default:
		edit = cfg.Edit
	}

	file := ""
	if fs.NArg() > 0 {
		file = fs.Arg(0)
	}

	if file == "" {
		cwd, err := env.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "mdp: %v\n", err)
			return 1
		}
		pick, err := env.FzfPick(context.Background(), cwd)
		if err != nil {
			// fzf isn't installed — there's no file to render and no way
			// to pick one. Show help on stdout (so it's pipe-friendly) and
			// exit 0; the help text already explains the fzf integration.
			fmt.Fprint(stdout, usage)
			return 0
		}
		if pick == "" {
			return 0
		}
		file = pick
	}

	src, err := filepath.Abs(file)
	if err != nil {
		fmt.Fprintf(stderr, "mdp: %v\n", err)
		return 1
	}
	info, err := env.Stat(src)
	if err != nil || info.IsDir() {
		fmt.Fprintf(stderr, "mdp: file not found: %s\n", src)
		return 1
	}

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

	body, err := render.RenderBody(src)
	if err != nil {
		fmt.Fprintf(stderr, "mdp: %v\n", err)
	}

	if latex.HasLatex(body) {
		if printPath {
			fmt.Fprintln(stderr, "mdp: -p/--print is not supported for LaTeX previews")
			return 1
		}
		return runStaticLatex(src, body, theme, cfg, env, stderr)
	}

	page := render.BuildPage(body, theme, 0, config.ExtraCSS(cfg, stderr), cfg.Colemak)

	tmpPath := tmpHTMLPath(env.TempDir(), src)
	if err := writeTmpFile(tmpPath, []byte(page)); err != nil {
		fmt.Fprintf(stderr, "mdp: writing tmp: %v\n", err)
		return 1
	}

	if printPath {
		fmt.Fprintln(stdout, tmpPath)
		return 0
	}

	argv := config.BrowserCmd(cfg.Browser, "file://"+tmpPath, env.LookPath, env.GOOS, stderr)
	if err := env.Spawn(argv); err != nil {
		fmt.Fprintf(stderr, "mdp: launching browser: %v\n", err)
		return 1
	}

	if !edit {
		return 0
	}

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

// tmpHTMLPath returns a stable path so re-runs on the same source
// overwrite rather than accumulate.
func tmpHTMLPath(tmpdir, src string) string {
	sum := sha1.Sum([]byte(src))
	digest := hex.EncodeToString(sum[:])[:12]
	return filepath.Join(tmpdir, "mdp-"+digest+".html")
}

// writeTmpFile writes data to path with mode 0600 and refuses to follow
// symlinks at the path. The stable filename in a shared /tmp is otherwise
// vulnerable to a foreign-user-planted symlink redirecting our truncate to
// e.g. ~/.bashrc; O_NOFOLLOW makes the open fail with ELOOP in that case.
// O_TRUNC is set so re-runs on the same source overwrite cleanly. Shared
// by `mdp <file>` (HTML preview) and `mdp skill path` (extracted reference).
func writeTmpFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// version is set at link time via `-ldflags "-X main.version=..."` for
// release builds (goreleaser) and `make install`. For module-mode
// `go install github.com/aldevv/md-preview/cmd/mdp@vX.Y.Z` invocations
// it stays empty and buildVersion falls back to debug.ReadBuildInfo.
var version string

// install.sh reads this to skip no-op reinstalls. Update also compares
// it against the latest release tag.
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

// spawnDetached starts argv in its own session so closing the terminal does
// not kill the browser. Output is discarded.
func spawnDetached(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty browser command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// runStaticLatex writes a per-preview HTML to the user cache dir and
// opens a native window. The child spawns an ephemeral 127.0.0.1
// HTTP server so WebKit gets the right Content-Type + a real origin,
// which enables streaming compile and JSC's wasm code cache. The
// gzip decompress runs in a background goroutine, overlapping with
// the page build + HTML write.
func runStaticLatex(src, body, theme string, cfg config.Config, env Environment, stderr io.Writer) int {
	cacheBase, err := mdpCacheDir()
	if err != nil {
		fmt.Fprintf(stderr, "mdp: cache dir: %v\n", err)
		return 1
	}
	type assetsResult struct {
		dir string
		err error
	}
	assetsCh := make(chan assetsResult, 1)
	go func() {
		dir, err := latex.WriteSiblingAssets(cacheBase)
		assetsCh <- assetsResult{dir, err}
	}()
	page := render.BuildPageWithAssets(body, theme, 0, config.ExtraCSS(cfg, stderr), cfg.Colemak, "./")
	r := <-assetsCh
	if r.err != nil {
		fmt.Fprintf(stderr, "mdp: writing latex assets: %v\n", r.err)
		return 1
	}
	htmlBase := htmlBasename(src)
	htmlPath := filepath.Join(r.dir, htmlBase)
	if err := writeTmpFile(htmlPath, []byte(page)); err != nil {
		fmt.Fprintf(stderr, "mdp: writing html: %v\n", err)
		return 1
	}
	if nativewin.Available() {
		if exe, err := env.Executable(); err == nil {
			if err := env.Spawn([]string{exe, "_open-window", r.dir, htmlBase}); err == nil {
				return 0
			}
		}
	}
	argv := config.BrowserCmd(cfg.Browser, "file://"+htmlPath, env.LookPath, env.GOOS, stderr)
	if err := env.Spawn(argv); err != nil {
		fmt.Fprintf(stderr, "mdp: launching browser: %v\n", err)
		return 1
	}
	return 0
}

// mdpCacheDir returns ~/.cache/mdp (or platform equivalent),
// creating it if needed. Persistent (survives /tmp cleanup) and gives
// the mdp:// scheme a stable IDB origin for the WebAssembly module
// cache.
func mdpCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "mdp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// htmlBasename returns a stable per-source filename so repeated runs
// on the same .tex overwrite rather than accumulate.
func htmlBasename(src string) string {
	sum := sha1.Sum([]byte(src))
	return "mdp-" + hex.EncodeToString(sum[:])[:12] + ".html"
}

// runOpenWindow is the child entry: args <assetsDir> <htmlBase>.
// Spins up a 127.0.0.1 HTTP server serving the assets dir with
// Cache-Control: immutable on wasm + JS, then opens the native
// window at http://127.0.0.1:PORT/<htmlBase>. http origin unlocks
// streaming compile + WebKit's wasm code cache. The port is
// persisted in the cache dir so the IDB origin stays stable across
// runs (otherwise every launch is a new origin, never a cache hit).
// Server lives only for the window's lifetime.
func runOpenWindow(args []string, stderr io.Writer, _ Environment) int {
	if len(args) < 2 {
		return 1
	}
	assetsDir, htmlBase := args[0], args[1]
	cacheBase, _ := mdpCacheDir()
	ln, err := listenStable(cacheBase)
	if err != nil {
		fmt.Fprintf(stderr, "mdp: listen: %v\n", err)
		return 1
	}
	port := ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: latexAssetServer(assetsDir), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	url := fmt.Sprintf("http://127.0.0.1:%d/%s", port, htmlBase)
	if err := nativewin.Open(nativewin.Options{URL: url, Title: "mdp"}); err != nil {
		fmt.Fprintf(stderr, "mdp: open-window: %v\n", err)
		return 1
	}
	return 0
}

// listenStable tries the port persisted in cacheBase/port first so
// the http://127.0.0.1:PORT IDB origin stays the same across runs.
// Falls back to a fresh ephemeral port (and persists it) when the
// previous one is in use.
func listenStable(cacheBase string) (net.Listener, error) {
	portFile := filepath.Join(cacheBase, "port")
	if data, err := os.ReadFile(portFile); err == nil {
		if p, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && p > 0 {
			if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
				return ln, nil
			}
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := ln.Addr().(*net.TCPAddr).Port
	_ = os.WriteFile(portFile, []byte(strconv.Itoa(p)), 0o644)
	return ln, nil
}

func latexAssetServer(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/")
		if rel == "" || strings.Contains(rel, "..") {
			http.NotFound(w, r)
			return
		}
		switch {
		case strings.HasSuffix(rel, ".wasm"):
			w.Header().Set("Content-Type", "application/wasm")
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case strings.HasSuffix(rel, ".js"):
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case strings.HasSuffix(rel, ".css"):
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		http.ServeFile(w, r, filepath.Join(dir, rel))
	})
}

// runWatchSubcommand handles `mdp watch [-t theme] [file]`. Picks a file
// (positional arg or fzf), validates theme, then runs the preview server
// with the editor-agnostic file watcher enabled.
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

	cfg, err := env.LoadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "mdp: config: %v\n", err)
	}

	file := ""
	if fs.NArg() > 0 {
		file = fs.Arg(0)
	}
	if file == "" {
		cwd, err := env.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "mdp: %v\n", err)
			return 1
		}
		pick, err := env.FzfPick(context.Background(), cwd)
		if err != nil {
			fmt.Fprintln(stderr, "mdp watch: pass a file or install fzf for the picker")
			return 1
		}
		if pick == "" {
			return 0
		}
		file = pick
	}

	src, err := filepath.Abs(file)
	if err != nil {
		fmt.Fprintf(stderr, "mdp: %v\n", err)
		return 1
	}
	info, err := env.Stat(src)
	if err != nil || info.IsDir() {
		fmt.Fprintf(stderr, "mdp: file not found: %s\n", src)
		return 1
	}

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

	opts := server.Options{
		File:    src,
		Port:    0, // kernel-assigned ephemeral port
		Theme:   theme,
		Colemak: cfg.Colemak,
		Watch:   true,
	}

	if envFlagOn("MDP_NATIVE") && env.OpenWindow != nil {
		return runWatchWithNativeWindow(opts, cfg, env, stderr)
	}
	return runWatchWithBrowser(opts, cfg, env, stderr)
}

// envFlagOn returns true for "1" or "true" so MDP_NATIVE matches the
// shape of MDP_COLEMAK (see runServe).
func envFlagOn(name string) bool {
	v := os.Getenv(name)
	return v == "1" || v == "true"
}

// runWatchWithBrowser is the default path: the server blocks the main
// goroutine, and OnListen asynchronously spawns the user's browser.
// Behavior matches mdp pre-native-window.
func runWatchWithBrowser(opts server.Options, cfg config.Config, env Environment, stderr io.Writer) int {
	opts.OnListen = func(port int) {
		url := fmt.Sprintf("http://localhost:%d/", port)
		argv := config.BrowserCmd(cfg.Browser, url, env.LookPath, env.GOOS, stderr)
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

// runWatchWithNativeWindow is the MDP_NATIVE=1 path: the server runs in
// a goroutine, the native window blocks the main goroutine, and we fall
// back to spawning the user's browser if the native path is unavailable.
//
// Cocoa requires the NSApp run loop on the OS main thread; main_darwin.go
// locks the main goroutine for that reason. GTK is more forgiving but
// nativewin.Open also locks for hygiene.
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

// runServe handles `mdp serve <file> <port> <theme>`. The Lua plugin spawns
// this and communicates over JSON-on-stdin; see internal/server. Colemak
// nav-key mode is opted in via MDP_COLEMAK=1 in the environment, with
// config.toml `colemak = true` as a fallback default.
func runServe(args []string, stderr io.Writer) int {
	if len(args) < 3 {
		fmt.Fprintln(stderr, "Usage: mdp serve <file> <port> <theme>")
		return 1
	}
	port, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "mdp serve: invalid port %q\n", args[1])
		return 1
	}
	colemak := false
	if cfg, _ := config.Load(); cfg.Colemak {
		colemak = true
	}
	if v := os.Getenv("MDP_COLEMAK"); v == "1" || v == "true" {
		colemak = true
	}
	opts := server.Options{
		File:    args[0],
		Port:    port,
		Theme:   args[2],
		Colemak: colemak,
	}
	if err := server.Run(opts); err != nil {
		fmt.Fprintf(stderr, "mdp serve: %v\n", err)
		return 1
	}
	return 0
}

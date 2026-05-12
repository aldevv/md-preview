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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"syscall"

	"github.com/aldevv/md-preview/internal/config"
	"github.com/aldevv/md-preview/internal/nativewin"
	"github.com/aldevv/md-preview/internal/render"
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

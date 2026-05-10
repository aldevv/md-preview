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
	"strconv"
	"syscall"

	"github.com/aldevv/md-preview/internal/config"
	"github.com/aldevv/md-preview/internal/render"
	"github.com/aldevv/md-preview/internal/server"
)

// Environment is the seam between run() and the OS — production wires it
// up via realEnv, tests substitute fakes.
type Environment struct {
	LookPath func(string) (string, error)
	GOOS     string
	Stat     func(string) (os.FileInfo, error)
	TempDir  func() string
	Getwd    func() (string, error)
	// FzfPick returns the user's pick (absolute or relative) or "" on
	// cancellation. Returns an error if fzf itself is unavailable.
	FzfPick func(ctx context.Context, cwd string) (string, error)
	// LoadConfig returns the parsed config and a non-nil error only on
	// parse failure (missing file is not an error).
	LoadConfig func() (config.Config, error)
	// Spawn launches a detached browser process. Tests substitute a no-op.
	Spawn func(argv []string) error
	// Exec replaces the current process (used for nvim handoff). Tests
	// substitute a recorder.
	Exec func(path string, argv []string, env []string) error
}

func realEnv() Environment {
	return Environment{
		LookPath:   exec.LookPath,
		GOOS:       runtime.GOOS,
		Stat:       os.Stat,
		TempDir:    os.TempDir,
		Getwd:      func() (string, error) { return os.Getwd() },
		FzfPick:    config.FzfPick,
		LoadConfig: config.Load,
		Spawn:      spawnDetached,
		Exec:       syscall.Exec,
	}
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
  mdp serve <file> <port> <theme>   Start the preview server (used by the
                                    md-preview.nvim Neovim plugin)
`

// run executes the CLI with the given args and IO. Returns the exit code.
func run(args []string, _ io.Reader, stdout, stderr io.Writer, env Environment) int {
	// Seed a commented default config on first run; idempotent thereafter.
	// Errors are non-fatal — the binary works fine without a config file.
	if err := config.EnsureDefault(); err != nil {
		fmt.Fprintf(stderr, "mdp: seeding default config: %v\n", err)
	}

	if len(args) > 0 {
		switch args[0] {
		case "serve":
			return runServe(args[1:], stderr)
		case "help":
			fmt.Fprint(stdout, usage)
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
	if err := writeTmpHTML(tmpPath, []byte(page)); err != nil {
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

// tmpHTMLPath returns a stable temp HTML path so re-runs on the same source
// overwrite rather than accumulate.
func tmpHTMLPath(tmpdir, src string) string {
	sum := sha1.Sum([]byte(src))
	digest := hex.EncodeToString(sum[:])[:12]
	return filepath.Join(tmpdir, "mdp-"+digest+".html")
}

// writeTmpHTML writes the page to path with mode 0600 and refuses to follow
// symlinks at the path. The stable filename in a shared /tmp is otherwise
// vulnerable to a foreign-user-planted symlink redirecting our truncate to
// e.g. ~/.bashrc; O_NOFOLLOW makes the open fail with ELOOP in that case.
// O_TRUNC is set so re-runs on the same source overwrite cleanly.
func writeTmpHTML(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
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
	if err := server.Run(args[0], port, args[2], colemak); err != nil {
		fmt.Fprintf(stderr, "mdp serve: %v\n", err)
		return 1
	}
	return 0
}

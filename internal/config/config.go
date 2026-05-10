// Package config loads the mdp TOML configuration and the small helpers
// (browser command resolution, fzf picker, extra CSS) that consume it.
// A missing config file is silent; malformed values produce warnings on
// the supplied error writer rather than failing the run.
package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the parsed TOML config. Fields use zero values / nil pointers to
// distinguish "unset" from explicitly-set values where it matters.
type Config struct {
	Theme     string   `toml:"theme"`
	FontSize  *float64 `toml:"font_size"`
	CustomCSS string   `toml:"custom_css"`
	Browser   any      `toml:"browser"`
	Edit      bool     `toml:"edit"`
	Colemak   bool     `toml:"colemak"`
}

// Path returns the resolved config file path, honoring XDG_CONFIG_HOME and
// falling back to ~/.config when it is unset.
func Path() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "md-preview", "config.toml")
}

// defaultConfigTemplate is the scaffold seeded by EnsureDefault. Every key
// is commented so seeding is behaviorally a no-op until the user opts in;
// the file's purpose is discoverability.
const defaultConfigTemplate = `# md-preview config — uncomment any line to override the built-in default.

# theme      = "dark"           # "dark" or "light"
# font_size  = 18               # body font-size in px
# custom_css = "~/path.css"     # appended after defaults; cascade wins
# browser    = "auto"           # "auto" | "firefox --new-window" | ["cmd", "arg"]
# edit       = false            # default for -e (also open nvim)
# colemak    = false            # swap in-page nav keys j/k/l → n/e/i
`

// EnsureDefault writes a commented default config file to Path() when one
// does not already exist. Existing files are left untouched. Failures
// (permission denied, etc.) are non-fatal: callers should ignore the
// returned error or surface it as a warning.
func EnsureDefault() error {
	path := Path()
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultConfigTemplate), 0o644)
}

// Load reads and parses the config from Path(). A missing file is not an
// error: callers get a zero Config and nil. Parse errors return a zero
// Config plus the error so callers can warn the user.
func Load() (Config, error) {
	var cfg Config
	path := Path()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	} else if err != nil {
		return cfg, err
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ExpandTilde replaces a leading "~/" with the user's home directory. Bare
// "~" and other inputs are returned unchanged.
func ExpandTilde(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~/"))
}

// ExtraCSS builds the CSS string contributed by the user's config. Errors
// are non-fatal: bad values are reported on errLog and skipped.
func ExtraCSS(cfg Config, errLog io.Writer) string {
	var parts []string

	if cfg.FontSize != nil {
		size := *cfg.FontSize
		if size > 0 {
			parts = append(parts, fmt.Sprintf("body { font-size: %spx; }", formatSize(size)))
		} else {
			fmt.Fprintf(errLog, "config: invalid font_size: %v\n", size)
		}
	}

	if cfg.CustomCSS != "" {
		path := ExpandTilde(cfg.CustomCSS)
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(errLog, "config: custom_css not found: %s\n", path)
		} else {
			parts = append(parts, string(data))
		}
	}

	return strings.Join(parts, "\n")
}

// formatSize renders a font size without a trailing ".0" when the value is a
// whole number, so "18" stays "18px" rather than "18.000000px".
func formatSize(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", v), "0"), ".")
}

// BrowserCmd builds the argv used to open url. The lookPath and goos
// arguments are injected so tests don't have to touch the real PATH or
// runtime.GOOS.
func BrowserCmd(browser any, url string, lookPath func(string) (string, error), goos string, errLog io.Writer) []string {
	switch b := browser.(type) {
	case nil:
		return autoBrowserCmd(url, lookPath, goos)
	case string:
		if b == "" || b == "auto" {
			return autoBrowserCmd(url, lookPath, goos)
		}
		return append(strings.Fields(b), url)
	case []string:
		out := make([]string, 0, len(b)+1)
		out = append(out, b...)
		return append(out, url)
	case []any:
		out := make([]string, 0, len(b)+1)
		for _, v := range b {
			if s, ok := v.(string); ok {
				out = append(out, s)
			} else {
				fmt.Fprintf(errLog, "config: invalid browser entry: %v\n", v)
				return autoBrowserCmd(url, lookPath, goos)
			}
		}
		return append(out, url)
	default:
		fmt.Fprintf(errLog, "config: invalid browser: %v\n", browser)
		return autoBrowserCmd(url, lookPath, goos)
	}
}

func autoBrowserCmd(url string, lookPath func(string) (string, error), goos string) []string {
	for _, c := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if p, err := lookPath(c); err == nil && p != "" {
			return []string{p, "--app=" + url}
		}
	}
	if goos == "darwin" {
		return []string{"open", url}
	}
	return []string{"xdg-open", url}
}

// FzfPick pipes a list of markdown files (cwd, recursive) into fzf and
// returns the user's pick. Cancellation returns "", nil.
func FzfPick(ctx context.Context, cwd string) (string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return "", errors.New("fzf not found on PATH")
	}

	var findCmd *exec.Cmd
	if p, err := exec.LookPath("fd"); err == nil {
		findCmd = exec.CommandContext(ctx, p, "-e", "md", "-t", "f")
	} else if p, err := exec.LookPath("fdfind"); err == nil {
		findCmd = exec.CommandContext(ctx, p, "-e", "md", "-t", "f")
	} else {
		findCmd = exec.CommandContext(ctx, "find", ".", "-type", "f", "-name", "*.md")
	}
	findCmd.Dir = cwd

	pipe, err := findCmd.StdoutPipe()
	if err != nil {
		return "", err
	}

	fzf := exec.CommandContext(ctx, "fzf")
	fzf.Dir = cwd
	fzf.Stdin = pipe
	fzf.Stderr = os.Stderr

	if err := findCmd.Start(); err != nil {
		return "", err
	}

	out, err := fzf.Output()
	_ = findCmd.Wait()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

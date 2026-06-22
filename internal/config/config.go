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
	Theme       string   `toml:"theme"`
	FontSize    *float64 `toml:"font_size"`
	CustomCSS   string   `toml:"custom_css"`
	Browser     any      `toml:"browser"`
	Edit        bool     `toml:"edit"`
	Colemak     bool     `toml:"colemak"`
	FileTree    bool     `toml:"file_tree"`
	FuzzyFinder bool     `toml:"fuzzy_finder"`
	Hop         bool     `toml:"hop"`
	Visual      bool     `toml:"visual"`
	Ask         bool     `toml:"ask"`
	AskCommand  string   `toml:"ask_command"`
	// AskTimeoutSec caps the ask subprocess wall-clock. 0 means
	// "use the built-in default" (60s). Negative or huge values
	// are clamped at the server.
	AskTimeoutSec int `toml:"ask_timeout_sec"`
	// AskSystemPrompt is prepended to every ask body so users can
	// supply a persona / domain instructions without rebuilding the
	// rest of the prompt template. Empty = no prefix.
	AskSystemPrompt string `toml:"ask_system_prompt"`
	// AskCardWidth / AskCardHeight size the answer popup. Width is
	// in CSS pixels, height in vh (1-100). 0 falls back to the
	// in-code defaults (560 / 50).
	AskCardWidth  int               `toml:"ask_card_width"`
	AskCardHeight int               `toml:"ask_card_height"`
	Keys          map[string]string `toml:"keys"`
	// PreferRunningBrowser: when true (default) and the user hasn't
	// pinned a browser, mdp skips the native window if a chromium-
	// family browser process is already running and routes the
	// preview to it via --app=. The warm browser opens a new window
	// in ~50-100ms vs. WebKit2GTK cold start of ~800-900ms. Set to
	// false (or MDP_NO_BROWSER_REUSE=1) to always go through the
	// native → browser fallback.
	PreferRunningBrowser *bool `toml:"prefer_running_browser"`
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
const defaultConfigTemplate = `# md-preview config: uncomment any line to override the built-in default.

# theme      = "dark"           # "dark" or "light"
# font_size  = 18               # body font-size in px
# custom_css = "~/path.css"     # appended after defaults; cascade wins
# browser    = "auto"           # "auto" | "firefox --new-window" | ["cmd", "arg"]
# edit       = false            # default for -e (also open nvim)
# colemak    = false            # swap in-page nav keys j/k/l → n/e/i
# file_tree    = true           # Tab toggles a sidebar listing previewable files
# fuzzy_finder = true           # Ctrl+P opens a fuzzy file finder
# hop    = true                 # 's' enters a hop.nvim-style char picker that jumps the caret
# visual = true                 # 'v' enters visual mode at the viewport center; h/l extend, 'y' yanks
# prefer_running_browser = true # if a chromium-family browser is already running, route the preview to it (faster than cold-starting the native window)

# [keys]
# down = "j"
# up = "k"
# left = "h"
# right = "l"
# tree_toggle = "Tab"
# tree_open = "Enter"
# finder_open = "Ctrl+p"
# select_pick = "s"
# select_visual = "v"
# zoom_in = "+"
# zoom_out = "-"
# zoom_reset = "0"

# ask              = true        # 'c' in visual mode (and the top-right star) sends the selection + a prompt to claude -p
# ask_command      = "claude -p" # command to spawn; receives the constructed prompt on stdin
# ask_timeout_sec  = 60          # hard cap on the spawned process (1-600)
# ask_system_prompt = ""         # extra instructions prepended to every ask body (persona, tone, etc.)
# ask_card_width   = 560         # answer popup max width in px
# ask_card_height  = 50          # answer popup max height in vh (1-100)
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
// error: callers get the defaults and nil. Parse errors return the
// defaults plus the error so callers can warn the user.
func Load() (Config, error) {
	cfg := defaults()
	path := Path()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	} else if err != nil {
		return cfg, err
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return defaults(), err
	}
	return cfg, nil
}

// defaults seeds the fields that should be on out-of-the-box; TOML
// decode merges user overrides over this struct, so an absent key keeps
// the default and an explicit `false` (or other zero value) wins.
func defaults() Config {
	return Config{FileTree: true, FuzzyFinder: true, Hop: true, Visual: true, Ask: true, AskCardWidth: 560, AskCardHeight: 50}
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

// runningChromiumComms maps the /proc/PID/comm string (truncated to
// TASK_COMM_LEN-1 = 15 chars) to the user-friendly launcher binary we
// look up on PATH. The /proc/PID/exe symlink usually resolves directly
// to the binary but the snap chromium wrapper and a few distros put
// the running process at a different path than the PATH entry, so we
// keep the PATH fallback.
var runningChromiumComms = map[string]string{
	"chrome":           "google-chrome",
	"chrome-stable":    "google-chrome-stable",
	"google-chrome":    "google-chrome",
	"chromium":         "chromium",
	"chromium-bro":     "chromium-browser",
	"chromium-browser": "chromium-browser",
	"brave":            "brave-browser",
	"brave-browser":    "brave-browser",
	"msedge":           "microsoft-edge",
	"microsoft-edge":   "microsoft-edge",
	"vivaldi-bin":      "vivaldi",
	"vivaldi-stable":   "vivaldi-stable",
}

// runningChromiumDarwinBasenames maps the basename of `ps -A -o comm=`
// output to itself; values exist so map presence checks are O(1).
// Helper processes (GPU/Renderer/Plugin) are filtered separately.
var runningChromiumDarwinBasenames = map[string]bool{
	"Google Chrome":        true,
	"Google Chrome Beta":   true,
	"Google Chrome Canary": true,
	"Chromium":             true,
	"Brave Browser":        true,
	"Brave Browser Beta":   true,
	"Microsoft Edge":       true,
	"Microsoft Edge Beta":  true,
	"Vivaldi":              true,
}

// runningChromiumWindowsImages maps tasklist image names to the PATH
// launcher we resolve back via lookPath. tasklist only prints the
// image filename (e.g. "chrome.exe"), not the path, so we go through
// LookPath to get something we can argv-spawn.
var runningChromiumWindowsImages = map[string]string{
	"chrome.exe":   "chrome",
	"chromium.exe": "chromium",
	"brave.exe":    "brave",
	"msedge.exe":   "msedge",
	"vivaldi.exe":  "vivaldi",
}

// isShellWrapperBin reports whether path looks like a shell interpreter
// rather than a browser binary. Some distros (snap brave / chromium,
// some flatpaks) ship the launcher as a shell script whose /proc/PID/exe
// resolves to the interpreter; spawning that with --app= silently no-ops,
// so the caller has to fall through to the lookPath fallback.
func isShellWrapperBin(path string) bool {
	switch filepath.Base(path) {
	case "bash", "sh", "dash", "zsh", "ksh", "fish":
		return true
	}
	return false
}

// RunningChromiumBin returns the executable path of a chromium-family
// browser process detected as running, or "" when none is found.
// Linux scans /proc/PID/comm; darwin runs `ps -A -o comm=`; windows
// runs `tasklist /FO CSV /NH` and resolves the matching .exe via
// PATH.
func RunningChromiumBin(lookPath func(string) (string, error), goos string) string {
	switch goos {
	case "linux":
		return runningChromiumLinux(lookPath)
	case "darwin":
		return runningChromiumDarwin()
	case "windows":
		return runningChromiumWindows(lookPath)
	}
	return ""
}

func runningChromiumLinux(lookPath func(string) (string, error)) string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}
		data, err := os.ReadFile("/proc/" + name + "/comm")
		if err != nil {
			continue
		}
		comm := strings.TrimSpace(string(data))
		launcher, ok := runningChromiumComms[comm]
		if !ok {
			continue
		}
		if exe, err := os.Readlink("/proc/" + name + "/exe"); err == nil && exe != "" {
			if !isShellWrapperBin(exe) {
				return exe
			}
		}
		if p, err := lookPath(launcher); err == nil && p != "" {
			return p
		}
	}
	return ""
}

func runningChromiumDarwin() string {
	out, err := exec.Command("ps", "-A", "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	for raw := range strings.SplitSeq(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		base := filepath.Base(line)
		// Skip helper processes (Google Chrome Helper, Chromium Helper
		// (GPU), etc.) so the launchable main bundle binary wins.
		if strings.Contains(base, " Helper") {
			continue
		}
		if runningChromiumDarwinBasenames[base] {
			return line
		}
	}
	return ""
}

func runningChromiumWindows(lookPath func(string) (string, error)) string {
	// tasklist CSV with no header. First field is the image name in
	// quotes: "chrome.exe","1234",...
	out, err := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return ""
	}
	for raw := range strings.SplitSeq(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || line[0] != '"' {
			continue
		}
		end := strings.Index(line[1:], `"`)
		if end < 0 {
			continue
		}
		image := strings.ToLower(line[1 : 1+end])
		launcher, ok := runningChromiumWindowsImages[image]
		if !ok {
			continue
		}
		if p, err := lookPath(launcher); err == nil && p != "" {
			return p
		}
	}
	return ""
}

// IsChromiumApp returns true when argv launches a chromium-family browser
// in --app= mode (the chromeless single-window UX the native fallback is
// trying to approximate). False for Firefox-family, xdg-open/open
// fallbacks, and any user-configured browser without --app=.
func IsChromiumApp(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	hasApp := false
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "--app=") {
			hasApp = true
			break
		}
	}
	if !hasApp {
		return false
	}
	bin := strings.ToLower(filepath.Base(argv[0]))
	for _, n := range []string{"chrome", "chromium", "brave", "edge", "vivaldi"} {
		if strings.Contains(bin, n) {
			return true
		}
	}
	return false
}

// autoBrowserFamilies lists browsers we know how to launch with a useful flag,
// in preference order. Chromium-family wins because --app= gives a chromeless
// single-window UX; Firefox-family is a normal new window since there's no
// equivalent app-mode that works without per-profile setup.
var autoBrowserFamilies = []struct {
	bins []string
	args func(url string) []string
}{
	{
		bins: []string{
			"google-chrome", "google-chrome-stable",
			"chromium", "chromium-browser",
			"brave-browser", "brave",
			"microsoft-edge", "microsoft-edge-stable",
			"vivaldi", "vivaldi-stable",
		},
		args: func(url string) []string { return []string{"--app=" + url} },
	},
	{
		bins: []string{"firefox", "firefox-esr", "librewolf", "waterfox"},
		args: func(url string) []string { return []string{"--new-window", url} },
	},
}

// autoMacAppBundles: chromium browsers on macOS ship as .app bundles with
// nothing on $PATH, so the PATH probe below misses them and a homebrew
// firefox shim wins. Probe these first on darwin to keep --app= mode.
var autoMacAppBundles = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
}

func autoBrowserCmd(url string, lookPath func(string) (string, error), goos string) []string {
	if goos == "darwin" {
		for _, p := range autoMacAppBundles {
			if _, err := lookPath(p); err == nil {
				return []string{p, "--app=" + url}
			}
		}
	}
	if goos == "windows" {
		for _, p := range autoWindowsAppPaths {
			if _, err := lookPath(p); err == nil {
				return []string{p, "--app=" + url}
			}
		}
	}
	for _, fam := range autoBrowserFamilies {
		for _, name := range fam.bins {
			if p, err := lookPath(name); err == nil && p != "" {
				return append([]string{p}, fam.args(url)...)
			}
		}
	}
	switch goos {
	case "darwin":
		return []string{"open", url}
	case "windows":
		// `start ""` opens url with the system-registered handler.
		// The empty "" is the window title (start treats the first
		// quoted arg as title), required so a URL with spaces doesn't
		// get misparsed.
		return []string{"cmd", "/c", "start", "", url}
	}
	return []string{"xdg-open", url}
}

// autoWindowsAppPaths: chromium browsers on Windows install under
// Program Files with stable layouts, but are usually not on PATH. Go's
// exec.LookPath on Windows also consults the "App Paths" registry key
// (where these installers register themselves), so a bare bin name in
// autoBrowserFamilies covers most setups, but probe these explicitly
// first to keep --app= mode preferred over a firefox shim that happens
// to live on PATH.
var autoWindowsAppPaths = []string{
	`C:\Program Files\Google\Chrome\Application\chrome.exe`,
	`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	`C:\Program Files\Chromium\Application\chrome.exe`,
	`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`,
	`C:\Program Files (x86)\BraveSoftware\Brave-Browser\Application\brave.exe`,
	`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	`C:\Program Files\Vivaldi\Application\vivaldi.exe`,
	`C:\Program Files (x86)\Vivaldi\Application\vivaldi.exe`,
}

// ChromiumPath returns the path to an installed Chromium-family browser
// suitable for `--headless --print-to-pdf`, or "" if none is found.
// Reuses the same families/bundles tables as autoBrowserCmd so PDF
// export tracks the same Chromium probe that powers the `--app=` flow.
func ChromiumPath(lookPath func(string) (string, error), goos string) string {
	if goos == "darwin" {
		for _, p := range autoMacAppBundles {
			if _, err := lookPath(p); err == nil {
				return p
			}
		}
	}
	for _, name := range autoBrowserFamilies[0].bins { // index 0 is chromium-family
		if p, err := lookPath(name); err == nil && p != "" {
			return p
		}
	}
	return ""
}

// FzfPick pipes a list of markdown files (cwd, recursive) into fzf and
// returns the user's pick. Cancellation returns "", nil.
func FzfPick(ctx context.Context, cwd string) (string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return "", errors.New("fzf not found on PATH")
	}

	// --hidden traverses dot-directories so notes kept under e.g. ~/.notes
	// or repo .github/ trees show up. fd still honours .gitignore by
	// default, which keeps node_modules and friends out of the picker.
	// `find` traverses hidden directories by default, so no extra flag.
	var findCmd *exec.Cmd
	if p, err := exec.LookPath("fd"); err == nil {
		findCmd = exec.CommandContext(ctx, p, "--hidden", "-e", "md", "-t", "f")
	} else if p, err := exec.LookPath("fdfind"); err == nil {
		findCmd = exec.CommandContext(ctx, p, "--hidden", "-e", "md", "-t", "f")
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

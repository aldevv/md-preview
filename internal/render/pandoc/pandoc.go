// Package pandoc renders pandoc-supported source (LaTeX, reStructured-
// Text, AsciiDoc, docx, odt, etc.) to HTML by shelling out to a host
// pandoc binary. See InputFormat for the full list of recognized
// extensions. mdp uses this for whole non-markdown files and for
// fenced ```latex blocks embedded in markdown.
package pandoc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/microcosm-cc/bluemonday"
)

// ErrNotFound is returned when the host binary isn't on PATH.
// Callers surface an install hint and exit non-zero.
var ErrNotFound = errors.New("pandoc: not found on PATH")

// ErrOutputTooLarge guards against runaway compiles producing
// pathologically large HTML.
var ErrOutputTooLarge = errors.New("pandoc: output exceeded size cap")

const (
	runTimeout = 5 * time.Second
	maxSize    = 20 * 1024 * 1024
)

var (
	probeMu sync.Mutex
	binPath string

	// hostFormatsMu / hostFormats memoize `pandoc --list-input-formats`
	// per binary path so Ensure doesn't reshell out on every render.
	hostFormatsMu sync.Mutex
	hostFormats   = map[string]map[string]bool{}

	sanitizer = func() *bluemonday.Policy {
		p := bluemonday.UGCPolicy()
		p.AllowAttrs("class").OnElements("span", "div", "code", "pre")
		return p
	}()
)

func hostSupports(bin, format string) bool {
	hostFormatsMu.Lock()
	defer hostFormatsMu.Unlock()
	set, ok := hostFormats[bin]
	if !ok {
		set = probeInputFormats(bin)
		hostFormats[bin] = set
	}
	return set[format]
}

func probeInputFormats(bin string) map[string]bool {
	out, err := exec.Command(bin, "--list-input-formats").Output()
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			set[name] = true
		}
	}
	return set
}

// Available probes cache first then $PATH. The auto-fetched cache
// wins because the pinned binary supports a known format set; the
// system pandoc may be older. Does NOT download; call Ensure for that.
func Available() bool {
	probeMu.Lock()
	defer probeMu.Unlock()
	if binPath != "" {
		return true
	}
	if p, ok := findCachedPandoc(); ok {
		binPath = p
		return true
	}
	if p, err := exec.LookPath("pandoc"); err == nil {
		binPath = p
		return true
	}
	return false
}

func ResetProbe() {
	probeMu.Lock()
	binPath = ""
	probeMu.Unlock()
	hostFormatsMu.Lock()
	hostFormats = map[string]map[string]bool{}
	hostFormatsMu.Unlock()
}

// Render returns sanitized HTML5. format is a pandoc --from name
// (defaults to "latex"). sourceDir becomes pandoc's CWD so
// \input{}/\includegraphics{} relative paths resolve; pass "" when
// rendering a fence with no surrounding file.
func Render(ctx context.Context, src []byte, sourceDir, format string) (string, error) {
	if !Available() {
		return "", ErrNotFound
	}
	if format == "" {
		format = "latex"
	}
	runCtx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	args := []string{
		"--from=" + format,
		"--to=html5",
		"--mathjax",
		"--no-highlight",
		"--sandbox",
	}
	// Without --citeproc, bibliography formats parse but emit empty
	// output (pandoc treats them as citation data, not prose).
	switch format {
	case "biblatex", "bibtex", "ris", "endnotexml", "csljson":
		args = append(args, "--citeproc")
	}
	cmd := exec.CommandContext(runCtx, binPath, args...)
	cmd.Dir = sourceDir
	cmd.Stdin = bytes.NewReader(src)
	var stdout, stderr bytes.Buffer
	lw := &limitWriter{w: &stdout, remaining: maxSize}
	cmd.Stdout = lw
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("pandoc: timed out after %s", runTimeout)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("pandoc: %s", msg)
		}
		return "", fmt.Errorf("pandoc: %w", err)
	}
	if lw.exceeded {
		return "", ErrOutputTooLarge
	}
	return sanitizer.Sanitize(stdout.String()), nil
}

// limitWriter caps writes to w. After the cap it drains silently so
// pandoc's pipe doesn't block.
type limitWriter struct {
	w         io.Writer
	remaining int
	exceeded  bool
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.remaining <= 0 {
		l.exceeded = true
		return len(p), nil
	}
	if len(p) > l.remaining {
		n, err := l.w.Write(p[:l.remaining])
		l.remaining -= n
		l.exceeded = true
		if err != nil {
			return n, err
		}
		return len(p), nil
	}
	n, err := l.w.Write(p)
	l.remaining -= n
	return n, err
}

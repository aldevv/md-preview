// Package latex renders LaTeX source to an HTML5 fragment by shelling
// out to pandoc. mdp uses this for whole .tex files and for fenced
// ```latex blocks embedded in markdown. Both paths funnel through
// Render so the pandoc invocation, resource caps, and HTML sanitization
// stay in one place.
package latex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
)

// sanitizer drops dangerous attributes and elements from pandoc's HTML
// output before it lands in the loopback-bound preview origin. Built
// once at package init; bluemonday policies are safe to share across
// goroutines.
var sanitizer = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// KaTeX-style math wrappers carry class="math inline" / "math display".
	// UGCPolicy already permits class on most elements, but be explicit
	// so a future bluemonday default-change doesn't break math rendering.
	p.AllowAttrs("class").OnElements("span", "div", "code", "pre")
	return p
}()

// ErrPandocNotFound is returned when the pandoc binary cannot be
// located on PATH (or at Options.PandocPath when set). Callers should
// surface a "install pandoc via apt/brew" message to the user.
var ErrPandocNotFound = errors.New("latex: pandoc not found on PATH")

// ErrOutputTooLarge is returned when pandoc's HTML output exceeds the
// configured cap. Likely culprits: pathological \input loops, huge
// generated tables.
var ErrOutputTooLarge = errors.New("latex: output exceeded size cap")

const (
	defaultTimeout = 5 * time.Second
	defaultMaxSize = 20 * 1024 * 1024 // 20 MiB
)

// Options is the value form so call sites can construct partial
// configurations without ceremony; defaults kick in on zero values.
type Options struct {
	// SourceDir is pandoc's working directory. \input{} and
	// \includegraphics{} resolve relative paths from here. Empty means
	// the caller's CWD.
	SourceDir string

	// Timeout bounds a single render. Zero falls back to defaultTimeout.
	Timeout time.Duration

	// PandocPath overrides the binary lookup. Empty means "pandoc" via
	// PATH.
	PandocPath string

	// MaxOutputBytes caps the HTML output size. Zero falls back to
	// defaultMaxSize.
	MaxOutputBytes int

	// Warnings, if non-nil, receives pandoc's stderr on successful
	// renders. Pandoc emits useful diagnostics (missing macros, unsafe
	// command rejected by --sandbox) here even on exit 0. Nil drops
	// them silently.
	Warnings io.Writer
}

// Render converts a LaTeX document to an HTML5 fragment.
//
// Math is emitted with \(...\) and \[...\] delimiters inside
// <span class="math inline|display"> wrappers (the --mathjax pandoc
// flag), matching what KaTeX's auto-render scans for.
func Render(ctx context.Context, src []byte, opts Options) (string, error) {
	bin := opts.PandocPath
	if bin == "" {
		bin = "pandoc"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return "", ErrPandocNotFound
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxSize := opts.MaxOutputBytes
	if maxSize <= 0 {
		maxSize = defaultMaxSize
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bin,
		"--from=latex",
		"--to=html5",
		"--mathjax",
		"--no-highlight",
		// --sandbox blocks file IO from pandoc filters and \input{}, plus
		// network fetches from \includegraphics{https://…}. Without it,
		// \input{/etc/passwd} silently reads any user-readable file and
		// inlines it into the loopback-bound preview, which would be an
		// exfiltration primitive when paired with any XSS path. Pandoc
		// >= 2.15.
		"--sandbox",
	)
	cmd.Dir = opts.SourceDir
	cmd.Stdin = bytes.NewReader(src)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lw := &limitWriter{w: &stdout, remaining: maxSize}
	cmd.Stdout = lw
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", ctx.Err()
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("latex: pandoc timed out after %s", timeout)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("latex: pandoc failed: %s", msg)
		}
		return "", fmt.Errorf("latex: pandoc failed: %w", err)
	}
	if lw.exceeded {
		return "", ErrOutputTooLarge
	}
	if opts.Warnings != nil && stderr.Len() > 0 {
		_, _ = opts.Warnings.Write(stderr.Bytes())
	}
	return sanitizer.Sanitize(stdout.String()), nil
}

// limitWriter caps the bytes written to w. After the cap is reached
// it pretends to accept the rest so pandoc keeps draining its stdout
// pipe and exits normally; exceeded is recorded for the caller to
// surface as ErrOutputTooLarge. Returning a non-nil error here would
// cause exec's io.Copy to stop and pandoc would block on a full pipe
// until the context timeout fires.
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

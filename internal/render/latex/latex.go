// Package latex renders LaTeX source to HTML by shelling out to a
// host pandoc binary. mdp uses this for whole .tex files and for
// fenced ```latex blocks embedded in markdown.
package latex

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

// ErrPandocNotFound is returned when the host binary isn't on PATH.
// Callers surface an install hint and exit non-zero.
var ErrPandocNotFound = errors.New("latex: pandoc not found on PATH")

// ErrOutputTooLarge guards against runaway compiles producing
// pathologically large HTML.
var ErrOutputTooLarge = errors.New("latex: output exceeded size cap")

const (
	pandocTimeout = 5 * time.Second
	pandocMaxSize = 20 * 1024 * 1024
)

var (
	pandocOnce sync.Once
	pandocBin  string

	sanitizer = func() *bluemonday.Policy {
		p := bluemonday.UGCPolicy()
		p.AllowAttrs("class").OnElements("span", "div", "code", "pre")
		return p
	}()
)

// PandocAvailable reports whether a usable pandoc binary is on PATH.
// Cached: the LookPath probe runs once per process.
func PandocAvailable() bool {
	pandocOnce.Do(func() {
		if p, err := exec.LookPath("pandoc"); err == nil {
			pandocBin = p
		}
	})
	return pandocBin != ""
}

// ResetPandocProbe clears the LookPath cache. Tests use this with
// t.Setenv("PATH", ...) to drive both pandoc-present and pandoc-
// missing paths in the same process.
func ResetPandocProbe() {
	pandocOnce = sync.Once{}
	pandocBin = ""
}

// Render shells out to host pandoc and returns sanitized HTML5.
// sourceDir is the pandoc CWD so \input{}/\includegraphics{} relative
// paths resolve correctly; pass "" for fences with no surrounding file.
func Render(ctx context.Context, src []byte, sourceDir string) (string, error) {
	if !PandocAvailable() {
		return "", ErrPandocNotFound
	}
	runCtx, cancel := context.WithTimeout(ctx, pandocTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, pandocBin,
		"--from=latex",
		"--to=html5",
		"--mathjax",
		"--no-highlight",
		"--sandbox",
	)
	cmd.Dir = sourceDir
	cmd.Stdin = bytes.NewReader(src)
	var stdout, stderr bytes.Buffer
	lw := &limitWriter{w: &stdout, remaining: pandocMaxSize}
	cmd.Stdout = lw
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("latex: pandoc timed out after %s", pandocTimeout)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("latex: pandoc: %s", msg)
		}
		return "", fmt.Errorf("latex: pandoc: %w", err)
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

package latex

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requirePandoc skips the test if pandoc isn't on PATH so the suite can
// run on machines / CI runners without it installed.
func requirePandoc(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pandoc"); err != nil {
		t.Skip("pandoc not on PATH; skipping")
	}
}

func TestRender_HappyPath(t *testing.T) {
	requirePandoc(t)

	src := []byte(`\section{Hello}
This is a paragraph with \emph{emphasis} and $E = mc^2$.

\begin{itemize}
\item one
\item two
\end{itemize}
`)

	html, err := Render(context.Background(), src, Options{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Collapse whitespace so assertions tolerate pandoc's line-wrapping
	// of long element tags. We only care that the substrings exist
	// in document order, not that they sit on the same line.
	flat := collapseWS(html)
	want := []string{
		`<h1`,
		`Hello`,
		`<em>emphasis</em>`,
		`class="math inline"`,
		`\(E = mc^2\)`,
		`<ul>`,
		`one`,
		`two`,
	}
	for _, sub := range want {
		if !strings.Contains(flat, sub) {
			t.Errorf("output missing %q\nfull output:\n%s", sub, html)
		}
	}
}

// collapseWS replaces runs of whitespace with a single space so HTML
// substring assertions can ignore pandoc's element-tag line wrapping.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func TestRender_DisplayMath(t *testing.T) {
	requirePandoc(t)

	src := []byte(`A display equation: $$\sum_{i=1}^n i = \frac{n(n+1)}{2}$$`)
	html, err := Render(context.Background(), src, Options{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(html, `class="math display"`) {
		t.Errorf("expected math display span, got:\n%s", html)
	}
	if !strings.Contains(html, `\[`) || !strings.Contains(html, `\]`) {
		t.Errorf("expected \\[...\\] delimiters preserved, got:\n%s", html)
	}
}

func TestRender_MissingPandoc(t *testing.T) {
	_, err := Render(context.Background(), []byte(`\section{x}`), Options{
		PandocPath: "definitely-not-pandoc-binary-xyz",
	})
	if !errors.Is(err, ErrPandocNotFound) {
		t.Errorf("expected ErrPandocNotFound, got %v", err)
	}
}

func TestRender_MalformedInput_DoesNotHang(t *testing.T) {
	requirePandoc(t)

	// Pandoc is lenient with malformed LaTeX (warnings to stderr, body
	// still produced). The test enforces a bounded wall time so a
	// future regression where bad input hangs the subprocess fails
	// loudly. Default 5s timeout caps the worst case.
	deadline, ok := t.Deadline()
	budget := 6 * time.Second
	if ok {
		budget = time.Until(deadline)
	}
	done := make(chan struct{})
	go func() {
		_, _ = Render(context.Background(), []byte(`\begin{itemize}\item one`), Options{})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(budget):
		t.Fatalf("Render hung past %s on malformed input", budget)
	}
}

func TestRender_Timeout(t *testing.T) {
	requirePandoc(t)

	// 1ns timeout always fires before pandoc can start. Whichever
	// surface error we see (timeout or process kill), Render must
	// return non-nil.
	_, err := Render(context.Background(), []byte(`\section{x}`), Options{
		Timeout: 1, // 1 nanosecond
	})
	if err == nil {
		t.Errorf("expected error from 1ns timeout, got nil")
	}
}

func TestRender_InputBlockedBySandbox(t *testing.T) {
	requirePandoc(t)

	// pandoc --sandbox is on by default in Render to block arbitrary
	// file reads via \input. A same-dir include should NOT make it
	// into the output, and the failure should be silent (no panic, no
	// hang) so a malicious README.tex can't probe the host fs.
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "secret.tex"),
		[]byte(`SECRET_TOKEN_DO_NOT_EMIT`), 0o600); err != nil {
		t.Fatal(err)
	}
	src := []byte(`Before. \input{secret} After.`)

	var warnings strings.Builder
	html, err := Render(context.Background(), src, Options{
		SourceDir: tmp,
		Warnings:  &warnings,
	})
	if err != nil {
		// Pandoc may either error out or render with the include omitted.
		// Both are acceptable; either way the secret must not leak.
		t.Logf("Render returned error (acceptable): %v", err)
	}
	if strings.Contains(html, "SECRET_TOKEN_DO_NOT_EMIT") {
		t.Errorf("sandbox bypass: secret leaked into output:\n%s", html)
	}
}

func TestLimitWriter_CapsAndDrains(t *testing.T) {
	// limitWriter must keep pretending to accept writes after the cap
	// so the upstream pipe doesn't block (would hang pandoc until the
	// context times out). The underlying buffer holds only `remaining`
	// bytes; the rest is dropped; exceeded flips to true.
	var sink strings.Builder
	lw := &limitWriter{w: &sink, remaining: 5}

	n, err := lw.Write([]byte("hello world"))
	if err != nil {
		t.Errorf("expected nil error on overflow (drain mode), got %v", err)
	}
	if n != 11 {
		t.Errorf("expected to consume all 11 bytes, got %d", n)
	}
	if sink.String() != "hello" {
		t.Errorf("expected sink=\"hello\", got %q", sink.String())
	}
	if !lw.exceeded {
		t.Errorf("expected exceeded=true after overflow")
	}

	// Subsequent writes also drain silently.
	n2, err := lw.Write([]byte("more"))
	if err != nil {
		t.Errorf("expected nil on follow-up drain, got %v", err)
	}
	if n2 != 4 {
		t.Errorf("expected to consume 4 bytes on follow-up, got %d", n2)
	}
	if sink.String() != "hello" {
		t.Errorf("sink mutated after cap: %q", sink.String())
	}
}

func TestRender_ParentCancel_SurfacesAsCanceled(t *testing.T) {
	requirePandoc(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	bigSrc := strings.Repeat(`\section{x}`+"\n", 10000)
	_, err := Render(ctx, []byte(bigSrc), Options{Timeout: 5 * time.Second})
	if err == nil {
		t.Fatalf("expected error from pre-cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRender_OutputTooLarge_TerminatesAndReturnsSentinel(t *testing.T) {
	requirePandoc(t)

	// 100-byte cap with a doc that pandoc renders to >100 bytes. The
	// drain-mode limitWriter should let pandoc finish (no pipe block),
	// and Render should return ErrOutputTooLarge.
	src := []byte(strings.Repeat("Some text with \\emph{words}.\n\n", 50))
	_, err := Render(context.Background(), src, Options{MaxOutputBytes: 100})
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Errorf("expected ErrOutputTooLarge, got %v", err)
	}
}

func TestRender_WarningsForwarded(t *testing.T) {
	requirePandoc(t)

	var warn strings.Builder
	// \input is blocked by --sandbox; pandoc emits a warning to stderr.
	_, err := Render(context.Background(), []byte(`\input{nope}`), Options{
		Warnings: &warn,
	})
	if err != nil {
		// Some pandoc versions error out instead of warning; either is
		// acceptable. The point of this test is to confirm Warnings is
		// not nil-deref'd when the writer is set.
		t.Logf("Render returned error (acceptable): %v", err)
	}
	// On success, warnings should land in our writer (not be silently
	// dropped). On error, we don't assert content.
}

func TestRender_InputBlocked_AbsolutePath(t *testing.T) {
	requirePandoc(t)

	// Absolute path \input must not read /etc/passwd. Even if pandoc
	// errors, the absolute-path content must not surface in the
	// returned HTML.
	src := []byte(`\input{/etc/passwd}`)
	html, err := Render(context.Background(), src, Options{})
	if err == nil && strings.Contains(html, "root:") {
		t.Errorf("sandbox bypass: /etc/passwd leaked into output:\n%s", html)
	}
}

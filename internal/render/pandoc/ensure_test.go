package pandoc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeFakePandoc writes a shell-script "pandoc" that responds to
// --list-input-formats with `formats` (one per line) and exits 0.
// Returns the binary path.
func writeFakePandoc(t *testing.T, dir, name string, formats []string) string {
	t.Helper()
	script := "#!/bin/sh\nif [ \"$1\" = \"--list-input-formats\" ]; then\n"
	for _, f := range formats {
		script += "echo " + f + "\n"
	}
	script += "fi\n"
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pandoc: %v", err)
	}
	return p
}

// stubCacheDir redirects os.UserCacheDir to a temp dir for the test
// duration. Returns the cache root so callers can place a fake cached
// pandoc under `<root>/mdp/pandoc-<Version>/pandoc`.
func stubCacheDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("HOME", tmp)
	return tmp
}

func TestEnsure_CacheWinsOverPath(t *testing.T) {
	ResetProbe()
	t.Cleanup(ResetProbe)

	cacheRoot := stubCacheDir(t)
	cacheDir := filepath.Join(cacheRoot, "mdp", "pandoc-"+Version)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cached := writeFakePandoc(t, cacheDir, "pandoc", []string{"latex"})

	pathDir := t.TempDir()
	writeFakePandoc(t, pathDir, "pandoc", []string{"latex"})
	t.Setenv("PATH", pathDir)

	got, err := Ensure(context.Background(), "latex", io.Discard)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != cached {
		t.Errorf("Ensure returned %q, want cached %q", got, cached)
	}
}

func TestEnsure_HostPandocSupportsFormat(t *testing.T) {
	ResetProbe()
	t.Cleanup(ResetProbe)
	stubCacheDir(t)

	pathDir := t.TempDir()
	host := writeFakePandoc(t, pathDir, "pandoc", []string{"latex", "rst"})
	t.Setenv("PATH", pathDir)

	got, err := Ensure(context.Background(), "rst", io.Discard)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != host {
		t.Errorf("Ensure returned %q, want host %q", got, host)
	}
}

func TestEnsure_HostMissingFormat_FallsBackToDownload(t *testing.T) {
	ResetProbe()
	t.Cleanup(ResetProbe)
	stubCacheDir(t)

	pathDir := t.TempDir()
	writeFakePandoc(t, pathDir, "pandoc", []string{"latex"}) // no djot
	t.Setenv("PATH", pathDir)

	called := false
	prev := downloadPandocFn
	t.Cleanup(func() { downloadPandocFn = prev })
	downloadPandocFn = func(ctx context.Context, stderr io.Writer) (string, error) {
		called = true
		return "/fake/auto-fetched/pandoc", nil
	}

	var stderr bytes.Buffer
	got, err := Ensure(context.Background(), "djot", &stderr)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != "/fake/auto-fetched/pandoc" {
		t.Errorf("Ensure returned %q, want fake download path", got)
	}
	if !called {
		t.Error("downloadPandocFn was not invoked")
	}
	if !bytes.Contains(stderr.Bytes(), []byte("does not support \"djot\"")) {
		t.Errorf("stderr missing fallback notice: %q", stderr.String())
	}
}

func TestEnsure_FormatUnsupportedByPinned(t *testing.T) {
	ResetProbe()
	t.Cleanup(ResetProbe)
	stubCacheDir(t)

	pathDir := t.TempDir()
	writeFakePandoc(t, pathDir, "pandoc", []string{"latex"})
	t.Setenv("PATH", pathDir)

	_, err := Ensure(context.Background(), "totally-not-a-format", io.Discard)
	if !errors.Is(err, ErrFormatUnsupported) {
		t.Errorf("err = %v, want ErrFormatUnsupported", err)
	}
}

func TestEnsure_NoHostPandoc_DownloadsForSupportedFormat(t *testing.T) {
	ResetProbe()
	t.Cleanup(ResetProbe)
	stubCacheDir(t)

	t.Setenv("PATH", "/nonexistent-dir")

	prev := downloadPandocFn
	t.Cleanup(func() { downloadPandocFn = prev })
	downloadPandocFn = func(ctx context.Context, stderr io.Writer) (string, error) {
		return "/fake/downloaded", nil
	}

	got, err := Ensure(context.Background(), "latex", io.Discard)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != "/fake/downloaded" {
		t.Errorf("Ensure returned %q, want fake downloaded path", got)
	}
}

func TestHostSupports_Memoizes(t *testing.T) {
	ResetProbe()
	t.Cleanup(ResetProbe)

	pathDir := t.TempDir()
	bin := writeFakePandoc(t, pathDir, "pandoc", []string{"latex", "rst"})

	if !hostSupports(bin, "rst") {
		t.Error("hostSupports(rst) returned false on first call")
	}
	// Overwrite the script to claim it supports nothing — if memoization
	// works, the second call still reports rst as supported.
	writeFakePandoc(t, pathDir, "pandoc", []string{})
	if !hostSupports(bin, "rst") {
		t.Error("hostSupports(rst) returned false after script changed; memo broken")
	}
}

func TestPinnedSupports_FromEmbeddedList(t *testing.T) {
	for _, f := range []string{"latex", "rst", "djot", "docx", "epub"} {
		if !PinnedSupports(f) {
			t.Errorf("PinnedSupports(%q) = false, want true (embedded list out of sync?)", f)
		}
	}
	if PinnedSupports("totally-fake-format") {
		t.Error("PinnedSupports(totally-fake-format) = true, want false")
	}
}

// Verify the fake-pandoc helper works on whatever platform tests run
// on. The other tests rely on /bin/sh shebangs being executable.
func TestFakePandocSanity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake pandoc relies on shebang exec")
	}
	dir := t.TempDir()
	bin := writeFakePandoc(t, dir, "pandoc", []string{"latex"})
	if !hostSupports(bin, "latex") {
		t.Errorf("fake pandoc didn't report latex as supported")
	}
}

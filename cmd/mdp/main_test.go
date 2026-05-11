package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldevv/md-preview/internal/config"
	"github.com/aldevv/md-preview/internal/server"
)

// testEnv returns an Environment safe for tests: no real fzf, no browser
// launch, no process replacement. Fields can be overridden per-test.
func testEnv(t *testing.T) Environment {
	t.Helper()
	return Environment{
		LookPath:   func(string) (string, error) { return "", errors.New("not found") },
		GOOS:       "linux",
		GOARCH:     "amd64",
		Stat:       os.Stat,
		TempDir:    t.TempDir,
		Getwd:      func() (string, error) { return ".", nil },
		FzfPick:    func(context.Context, string) (string, error) { return "", errors.New("fzf not found on PATH") },
		LoadConfig: func() (config.Config, error) { return config.Config{}, nil },
		Spawn:      func([]string) error { return nil },
		Exec:       func(string, []string, []string) error { return nil },
		RunServer:  func(server.Options) error { return nil },
		Executable: func() (string, error) { return "", errors.New("not stubbed") },
		HTTPGet:    func(string) (io.ReadCloser, error) { return nil, errors.New("not stubbed") },
		RunCmd:     func(string, []string, []string) error { return errors.New("not stubbed") },
	}
}

// writeMD writes a markdown file in t.TempDir() and returns its absolute path.
func writeMD(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRun_NoArgs_NoFzf_ShowsHelp(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	code := run(nil, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "Usage: mdp") {
		t.Fatalf("stdout = %q, want help text starting with 'Usage: mdp'", out.String())
	}
	if !strings.Contains(out.String(), "fzf") {
		t.Fatalf("help output should mention fzf integration; got %q", out.String())
	}
}

func TestRun_HelpSubcommand(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	code := run([]string{"help"}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "Usage: mdp") {
		t.Fatalf("stdout = %q, want help text", out.String())
	}
}

func TestRun_HelpFlag(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	code := run([]string{"-h"}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("-h exit code = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "Usage: mdp") {
		t.Fatalf("stdout = %q, want help text", out.String())
	}
}

func TestRun_FileMissing(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	missing := filepath.Join(t.TempDir(), "nope.md")
	code := run([]string{missing}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "file not found") {
		t.Fatalf("stderr = %q, want 'file not found'", errb.String())
	}
}

func TestRun_PrintMode(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	src := writeMD(t, "# hello\n\nworld\n")
	code := run([]string{"-p", src}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, errb.String())
	}
	tmpPath := strings.TrimSpace(out.String())
	if tmpPath == "" {
		t.Fatal("stdout did not contain a path")
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("reading written tmp file: %v", err)
	}
	if !strings.Contains(string(data), "<html") {
		t.Fatalf("tmp file is missing <html: %s", string(data[:min(len(data), 200)]))
	}
}

func TestRun_ThemeFlag(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	src := writeMD(t, "hi\n")
	code := run([]string{"-p", "-t", "light", src}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr=%s", code, errb.String())
	}
	data, err := os.ReadFile(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "#ffffff") {
		t.Fatalf("light theme color not found in page")
	}
}

func TestRun_ThemeFlag_Default(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	src := writeMD(t, "hi\n")
	code := run([]string{"-p", src}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr=%s", code, errb.String())
	}
	data, err := os.ReadFile(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "#0d1117") {
		t.Fatalf("dark theme color not found in page")
	}
}

func TestRun_BadTheme(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	src := writeMD(t, "hi\n")
	code := run([]string{"-p", "-t", "purple", src}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "invalid theme") {
		t.Fatalf("stderr = %q, want 'invalid theme'", errb.String())
	}
	data, err := os.ReadFile(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "#0d1117") {
		t.Fatalf("expected fallback to dark theme")
	}
}

func TestRun_EditAndNoEdit_Conflict(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	src := writeMD(t, "hi\n")
	code := run([]string{"-e", "--no-edit", src}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "conflict") {
		t.Fatalf("stderr = %q, want 'conflict'", errb.String())
	}
}

func TestRun_WatchSubcommand_InvokesServer(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	src := writeMD(t, "# hi\n")

	var (
		gotOpts   server.Options
		serverHit bool
	)
	env.RunServer = func(opts server.Options) error {
		serverHit = true
		gotOpts = opts
		// Simulate the kernel binding a port so the browser branch fires.
		if opts.OnListen != nil {
			opts.OnListen(45678)
		}
		return nil
	}

	var spawnedArgv []string
	env.Spawn = func(argv []string) error {
		spawnedArgv = argv
		return nil
	}
	env.LookPath = func(name string) (string, error) {
		if name == "xdg-open" {
			return "/usr/bin/xdg-open", nil
		}
		return "", errors.New("not found")
	}

	code := run([]string{"watch", src}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr=%s", code, errb.String())
	}
	if !serverHit {
		t.Fatal("RunServer was not called")
	}
	if !gotOpts.Watch {
		t.Errorf("server.Options.Watch = false, want true")
	}
	if gotOpts.Port != 0 {
		t.Errorf("server.Options.Port = %d, want 0 (ephemeral)", gotOpts.Port)
	}
	if gotOpts.File == "" || !strings.HasSuffix(gotOpts.File, "doc.md") {
		t.Errorf("server.Options.File = %q, want absolute path ending in doc.md", gotOpts.File)
	}
	joined := strings.Join(spawnedArgv, " ")
	if !strings.Contains(joined, "http://localhost:45678/") {
		t.Errorf("browser argv = %v, want http://localhost:45678/", spawnedArgv)
	}
}

func TestRun_WatchSubcommand_ThemeFlag(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	src := writeMD(t, "# hi\n")
	var gotOpts server.Options
	env.RunServer = func(opts server.Options) error {
		gotOpts = opts
		return nil
	}
	code := run([]string{"watch", "-t", "light", src}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr=%s", code, errb.String())
	}
	if gotOpts.Theme != "light" {
		t.Errorf("Theme = %q, want light", gotOpts.Theme)
	}
}

func TestRun_WatchSubcommand_FileMissing(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	missing := filepath.Join(t.TempDir(), "nope.md")
	code := run([]string{"watch", missing}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "file not found") {
		t.Fatalf("stderr = %q, want 'file not found'", errb.String())
	}
}

func TestRun_ServeSubcommand_TooFewArgs(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	code := run([]string{"serve", "file.md"}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "Usage: mdp serve") {
		t.Fatalf("stderr = %q, want serve usage", errb.String())
	}
}

func TestRun_ServeSubcommand_InvalidPort(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	code := run([]string{"serve", "file.md", "notaport", "dark"}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "invalid port") {
		t.Fatalf("stderr = %q, want invalid port", errb.String())
	}
}

func TestRun_TempFilenameStable(t *testing.T) {
	var out1, out2, errb bytes.Buffer
	env := testEnv(t)
	tmpdir := t.TempDir()
	env.TempDir = func() string { return tmpdir }
	src := writeMD(t, "stable\n")

	if code := run([]string{"-p", src}, nil, &out1, &errb, env); code != 0 {
		t.Fatalf("first run exit %d; stderr=%s", code, errb.String())
	}
	if code := run([]string{"-p", src}, nil, &out2, &errb, env); code != 0 {
		t.Fatalf("second run exit %d; stderr=%s", code, errb.String())
	}
	a := strings.TrimSpace(out1.String())
	b := strings.TrimSpace(out2.String())
	if a == "" || a != b {
		t.Fatalf("tmp paths differ: %q vs %q", a, b)
	}
}

func TestRun_SkillPath_ExtractsReference(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	tmpdir := t.TempDir()
	env.TempDir = func() string { return tmpdir }

	code := run([]string{"skill", "path"}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, errb.String())
	}
	dest := strings.TrimSpace(out.String())
	if dest == "" {
		t.Fatal("stdout did not contain a path")
	}
	if filepath.Dir(dest) != tmpdir {
		t.Errorf("dest %q not under TempDir %q", dest, tmpdir)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading extracted ref: %v", err)
	}
	if !strings.Contains(string(data), "mdp skill reference") {
		t.Fatalf("extracted file missing reference header; got %q", string(data[:min(len(data), 200)]))
	}
}

func TestRun_SkillPath_RerunOverwritesSamePath(t *testing.T) {
	var out1, out2, errb bytes.Buffer
	env := testEnv(t)
	tmpdir := t.TempDir()
	env.TempDir = func() string { return tmpdir }

	if code := run([]string{"skill", "path"}, nil, &out1, &errb, env); code != 0 {
		t.Fatalf("first run exit %d; stderr=%s", code, errb.String())
	}
	if code := run([]string{"skill", "path"}, nil, &out2, &errb, env); code != 0 {
		t.Fatalf("second run exit %d; stderr=%s", code, errb.String())
	}
	a := strings.TrimSpace(out1.String())
	b := strings.TrimSpace(out2.String())
	if a == "" || a != b {
		t.Fatalf("skill paths differ across runs: %q vs %q", a, b)
	}
}

func TestRun_SkillPath_BadSubcommand(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	code := run([]string{"skill", "wat"}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("stderr = %q, want 'unknown command'", errb.String())
	}
}

func TestRun_SkillNoArgs_ShowsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	code := run([]string{"skill"}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "Usage: mdp skill") {
		t.Fatalf("stdout = %q, want skill usage", out.String())
	}
}

func TestRun_SkillHelp_ShowsUsage(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		var out, errb bytes.Buffer
		env := testEnv(t)
		code := run([]string{"skill", arg}, nil, &out, &errb, env)
		if code != 0 {
			t.Fatalf("`skill %s` exit code = %d, want 0; stderr=%s", arg, code, errb.String())
		}
		if !strings.Contains(out.String(), "Usage: mdp skill") {
			t.Fatalf("`skill %s` stdout = %q, want skill usage", arg, out.String())
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// fakeGetter returns canned bodies per URL substring; an unknown URL
// returns an error so tests catch unexpected fetches.
func fakeGetter(t *testing.T, hits map[string][]byte) func(string) (io.ReadCloser, error) {
	t.Helper()
	return func(url string) (io.ReadCloser, error) {
		for k, v := range hits {
			if strings.Contains(url, k) {
				return io.NopCloser(bytes.NewReader(v)), nil
			}
		}
		return nil, errors.New("unexpected URL: " + url)
	}
}

// tarballWithMdp returns a gzipped tar containing a single "mdp" entry
// with the given body, mimicking the goreleaser archive layout.
func tarballWithMdp(t *testing.T, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "mdp", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// checksumsFor builds a goreleaser-style checksums.txt body matching the
// given (name -> bytes) entries.
func checksumsFor(entries map[string][]byte) []byte {
	var sb strings.Builder
	for name, data := range entries {
		sum := sha256.Sum256(data)
		fmt.Fprintf(&sb, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return []byte(sb.String())
}

// setVersion overrides the package-level `version` var for the duration
// of a test so buildVersion() returns a deterministic, regex-valid tag.
func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestRun_UpdateCheck_ReportsAvailable(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "v0.1.0")
	env := testEnv(t)
	env.HTTPGet = fakeGetter(t, map[string][]byte{
		"releases/latest": []byte(`{"tag_name":"v9.9.9"}`),
	})
	code := run([]string{"update", "--check"}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "v9.9.9 available") {
		t.Fatalf("stdout=%q, want 'v9.9.9 available'", out.String())
	}
}

func TestRun_UpdateCheck_AlreadyAtLatest(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "v9.9.9")
	env := testEnv(t)
	env.HTTPGet = fakeGetter(t, map[string][]byte{
		"releases/latest": []byte(`{"tag_name":"v9.9.9"}`),
	})
	code := run([]string{"update", "--check"}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "already at v9.9.9") {
		t.Fatalf("stdout=%q, want 'already at v9.9.9'", out.String())
	}
}

func TestRun_Update_GoInstallPath(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "v0.1.0")
	env := testEnv(t)
	tmpdir := t.TempDir()
	dest := filepath.Join(tmpdir, "mdp")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	env.Executable = func() (string, error) { return dest, nil }
	env.LookPath = func(name string) (string, error) {
		if name == "go" {
			return "/usr/bin/go", nil
		}
		return "", errors.New("not found")
	}
	env.HTTPGet = fakeGetter(t, map[string][]byte{
		"releases/latest": []byte(`{"tag_name":"v9.9.9"}`),
	})

	var (
		gotName string
		gotArgs []string
		gotEnv  []string
	)
	env.RunCmd = func(name string, args, environ []string) error {
		gotName, gotArgs, gotEnv = name, args, environ
		return nil
	}

	code := run([]string{"update"}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	if gotName != "go" {
		t.Errorf("cmd name = %q, want go", gotName)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "install" || !strings.HasSuffix(gotArgs[1], "@v9.9.9") {
		t.Errorf("cmd args = %v, want [install <module>@v9.9.9]", gotArgs)
	}
	if !strings.HasPrefix(gotArgs[1], updateGoModule+"@") {
		t.Errorf("module path = %q, want prefix %q@", gotArgs[1], updateGoModule)
	}
	foundGobin := false
	for _, e := range gotEnv {
		if e == "GOBIN="+tmpdir {
			foundGobin = true
			break
		}
	}
	if !foundGobin {
		t.Errorf("GOBIN=%s not in passed environ", tmpdir)
	}
}

func TestRun_Update_GoInstallFailsReturns1(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "v0.1.0")
	env := testEnv(t)
	tmpdir := t.TempDir()
	dest := filepath.Join(tmpdir, "mdp")
	_ = os.WriteFile(dest, []byte("old"), 0o755)
	env.Executable = func() (string, error) { return dest, nil }
	env.LookPath = func(name string) (string, error) {
		if name == "go" {
			return "/usr/bin/go", nil
		}
		return "", errors.New("not found")
	}
	env.HTTPGet = fakeGetter(t, map[string][]byte{
		"releases/latest": []byte(`{"tag_name":"v9.9.9"}`),
	})
	env.RunCmd = func(string, []string, []string) error {
		return errors.New("boom")
	}
	code := run([]string{"update"}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "go install") || !strings.Contains(errb.String(), "failed: boom") {
		t.Fatalf("stderr=%q, want 'go install ... failed: boom'", errb.String())
	}
}

func TestRun_Update_TarballReplacesBinary(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "v0.1.0")
	env := testEnv(t)
	tmpdir := t.TempDir()
	dest := filepath.Join(tmpdir, "mdp")
	if err := os.WriteFile(dest, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	env.Executable = func() (string, error) { return dest, nil }
	env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	tarball := tarballWithMdp(t, []byte("NEW_BINARY"))
	env.HTTPGet = fakeGetter(t, map[string][]byte{
		"releases/latest":        []byte(`{"tag_name":"v9.9.9"}`),
		"checksums.txt":          checksumsFor(map[string][]byte{"mdp_linux_amd64.tar.gz": tarball}),
		"mdp_linux_amd64.tar.gz": tarball,
	})

	code := run([]string{"update"}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW_BINARY" {
		t.Errorf("binary content = %q, want NEW_BINARY", got)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("binary mode = %v, want executable", info.Mode())
	}
}

func TestRun_Update_TarballUnsupportedArch(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "v0.1.0")
	env := testEnv(t)
	env.GOARCH = "riscv64"
	tmpdir := t.TempDir()
	dest := filepath.Join(tmpdir, "mdp")
	_ = os.WriteFile(dest, []byte("OLD"), 0o755)
	env.Executable = func() (string, error) { return dest, nil }
	env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	env.HTTPGet = fakeGetter(t, map[string][]byte{
		"releases/latest": []byte(`{"tag_name":"v9.9.9"}`),
	})
	code := run([]string{"update"}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "unsupported arch") {
		t.Fatalf("stderr=%q, want 'unsupported arch'", errb.String())
	}
}

func TestRun_Update_UnexpectedArg(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	code := run([]string{"update", "extra"}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "unexpected arg") {
		t.Fatalf("stderr=%q, want 'unexpected arg'", errb.String())
	}
}

func TestRun_Update_BadFlag(t *testing.T) {
	var out, errb bytes.Buffer
	env := testEnv(t)
	code := run([]string{"update", "--nope"}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestRun_Update_DevelBuildRefusedWithoutForce(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "")
	env := testEnv(t)
	env.HTTPGet = fakeGetter(t, map[string][]byte{
		"releases/latest": []byte(`{"tag_name":"v9.9.9"}`),
	})
	code := run([]string{"update"}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "refusing to overwrite") {
		t.Fatalf("stderr=%q, want 'refusing to overwrite'", errb.String())
	}
}

func TestRun_Update_DevelBuildProceedsWithForce(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "")
	env := testEnv(t)
	tmpdir := t.TempDir()
	dest := filepath.Join(tmpdir, "mdp")
	_ = os.WriteFile(dest, []byte("OLD"), 0o755)
	env.Executable = func() (string, error) { return dest, nil }
	env.LookPath = func(name string) (string, error) {
		if name == "go" {
			return "/usr/bin/go", nil
		}
		return "", errors.New("not found")
	}
	env.HTTPGet = fakeGetter(t, map[string][]byte{
		"releases/latest": []byte(`{"tag_name":"v9.9.9"}`),
	})
	env.RunCmd = func(string, []string, []string) error { return nil }
	code := run([]string{"update", "--force"}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
}

func TestRun_Update_PinnedVersion(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "v0.1.0")
	env := testEnv(t)
	tmpdir := t.TempDir()
	dest := filepath.Join(tmpdir, "mdp")
	_ = os.WriteFile(dest, []byte("OLD"), 0o755)
	env.Executable = func() (string, error) { return dest, nil }
	env.LookPath = func(name string) (string, error) {
		if name == "go" {
			return "/usr/bin/go", nil
		}
		return "", errors.New("not found")
	}
	env.HTTPGet = func(string) (io.ReadCloser, error) {
		t.Fatal("HTTPGet should not fire when --version is pinned")
		return nil, nil
	}
	var gotArgs []string
	env.RunCmd = func(_ string, args, _ []string) error {
		gotArgs = args
		return nil
	}
	code := run([]string{"update", "--version", "v0.2.0"}, nil, &out, &errb, env)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errb.String())
	}
	if len(gotArgs) != 2 || !strings.HasSuffix(gotArgs[1], "@v0.2.0") {
		t.Errorf("go install args = %v, want suffix @v0.2.0", gotArgs)
	}
}

func TestRun_Update_RejectsInvalidPinnedVersion(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "v0.1.0")
	env := testEnv(t)
	code := run([]string{"update", "--version", "rm -rf /"}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "invalid --version") {
		t.Fatalf("stderr=%q, want 'invalid --version'", errb.String())
	}
}

func TestRun_Update_RejectsUntrustedTagFromAPI(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "v0.1.0")
	env := testEnv(t)
	env.HTTPGet = fakeGetter(t, map[string][]byte{
		"releases/latest": []byte(`{"tag_name":"../../etc/passwd"}`),
	})
	code := run([]string{"update", "--check"}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "untrusted tag") {
		t.Fatalf("stderr=%q, want 'untrusted tag'", errb.String())
	}
}

func TestRun_Update_ChecksumMismatchAborts(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "v0.1.0")
	env := testEnv(t)
	tmpdir := t.TempDir()
	dest := filepath.Join(tmpdir, "mdp")
	_ = os.WriteFile(dest, []byte("OLD"), 0o755)
	env.Executable = func() (string, error) { return dest, nil }
	env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	tarball := tarballWithMdp(t, []byte("NEW_BINARY"))
	env.HTTPGet = fakeGetter(t, map[string][]byte{
		"releases/latest":        []byte(`{"tag_name":"v9.9.9"}`),
		"checksums.txt":          []byte("deadbeef" + strings.Repeat("0", 56) + "  mdp_linux_amd64.tar.gz\n"),
		"mdp_linux_amd64.tar.gz": tarball,
	})
	code := run([]string{"update"}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "checksum mismatch") {
		t.Fatalf("stderr=%q, want 'checksum mismatch'", errb.String())
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "OLD" {
		t.Errorf("binary changed despite checksum mismatch: %q", got)
	}
}

func TestRun_Update_TarballSkipsNonRegularMdpEntry(t *testing.T) {
	var out, errb bytes.Buffer
	setVersion(t, "v0.1.0")
	env := testEnv(t)
	tmpdir := t.TempDir()
	dest := filepath.Join(tmpdir, "mdp")
	_ = os.WriteFile(dest, []byte("OLD"), 0o755)
	env.Executable = func() (string, error) { return dest, nil }
	env.LookPath = func(string) (string, error) { return "", errors.New("not found") }

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "mdp", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	_ = tw.Close()
	_ = gz.Close()

	env.HTTPGet = fakeGetter(t, map[string][]byte{
		"releases/latest":        []byte(`{"tag_name":"v9.9.9"}`),
		"checksums.txt":          checksumsFor(map[string][]byte{"mdp_linux_amd64.tar.gz": buf.Bytes()}),
		"mdp_linux_amd64.tar.gz": buf.Bytes(),
	})
	code := run([]string{"update"}, nil, &out, &errb, env)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "mdp binary not found") {
		t.Fatalf("stderr=%q, want 'mdp binary not found'", errb.String())
	}
}

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldevv/md-preview/internal/config"
)

// testEnv returns an Environment safe for tests: no real fzf, no browser
// launch, no process replacement. Fields can be overridden per-test.
func testEnv(t *testing.T) Environment {
	t.Helper()
	return Environment{
		LookPath:   func(string) (string, error) { return "", errors.New("not found") },
		GOOS:       "linux",
		Stat:       os.Stat,
		TempDir:    t.TempDir,
		Getwd:      func() (string, error) { return ".", nil },
		FzfPick:    func(context.Context, string) (string, error) { return "", errors.New("fzf not found on PATH") },
		LoadConfig: func() (config.Config, error) { return config.Config{}, nil },
		Spawn:      func([]string) error { return nil },
		Exec:       func(string, []string, []string) error { return nil },
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

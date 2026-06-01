package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPDF_FileNotFound(t *testing.T) {
	env := testEnv(t)
	var out, errb bytes.Buffer
	code := runPDF([]string{"/nonexistent/foo.md"}, &out, &errb, env)
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "file not found") {
		t.Errorf("expected 'file not found' in stderr, got %q", errb.String())
	}
}

func TestRunPDF_MissingChromiumShowsInstallHint(t *testing.T) {
	env := testEnv(t)
	// LookPath stubbed to always fail in testEnv -> ChromiumPath returns "".
	src := writeMD(t, "# Hi\n")

	var out, errb bytes.Buffer
	code := runPDF([]string{src}, &out, &errb, env)
	if code != 1 {
		t.Errorf("expected exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "Chromium not found") &&
		!strings.Contains(errb.String(), "Chrome/Chromium not found") {
		t.Errorf("expected chromium-missing hint in stderr, got %q", errb.String())
	}
}

func TestRunPDF_DefaultOutputPath(t *testing.T) {
	env := testEnv(t)
	env.LookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}
	var capturedArgs []string
	env.RunCmd = func(_ string, args, _ []string) error {
		capturedArgs = args
		return nil
	}

	src := writeMD(t, "# Default output\n")
	expectedOut := strings.TrimSuffix(src, filepath.Ext(src)) + ".pdf"

	var out, errb bytes.Buffer
	code := runPDF([]string{src}, &out, &errb, env)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, errb.String())
	}
	want := "--print-to-pdf=" + expectedOut
	if !containsArg(capturedArgs, want) {
		t.Errorf("expected argv to contain %q, got %v", want, capturedArgs)
	}
	if strings.TrimSpace(out.String()) != expectedOut {
		t.Errorf("expected stdout = output path %q, got %q", expectedOut, out.String())
	}
}

func TestRunPDF_OutputFlagRespected(t *testing.T) {
	env := testEnv(t)
	env.LookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}
	var capturedArgs []string
	env.RunCmd = func(_ string, args, _ []string) error {
		capturedArgs = args
		return nil
	}

	src := writeMD(t, "# Custom output\n")
	dir := filepath.Dir(src)
	custom := filepath.Join(dir, "custom-name.pdf")

	var out, errb bytes.Buffer
	// Go's flag.Parse stops at the first non-flag positional, so -o
	// must come BEFORE the source path.
	code := runPDF([]string{"-o", custom, src}, &out, &errb, env)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, errb.String())
	}
	if !containsArg(capturedArgs, "--print-to-pdf="+custom) {
		t.Errorf("expected argv to contain --print-to-pdf=%s, got %v", custom, capturedArgs)
	}
}

func TestRunPDF_ChromeFailurePropagates(t *testing.T) {
	env := testEnv(t)
	env.LookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}
	env.RunCmd = func(string, []string, []string) error {
		return &execErr{msg: "exit 137"}
	}
	src := writeMD(t, "# x\n")

	var out, errb bytes.Buffer
	code := runPDF([]string{src}, &out, &errb, env)
	if code != 1 {
		t.Errorf("expected exit 1 on chrome failure, got %d", code)
	}
	if !strings.Contains(errb.String(), "exit 137") {
		t.Errorf("expected chrome error in stderr, got %q", errb.String())
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

type execErr struct{ msg string }

func (e *execErr) Error() string { return e.msg }

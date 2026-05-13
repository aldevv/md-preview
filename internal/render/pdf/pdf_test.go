package pdf

import (
	"errors"
	"strings"
	"testing"
)

func TestRender_NoChromiumBin(t *testing.T) {
	err := Render("/tmp/x.html", "/tmp/x.pdf", "", func(string, []string, []string) error {
		t.Errorf("run should not be invoked when chromiumBin is empty")
		return nil
	})
	if !errors.Is(err, ErrChromiumNotFound) {
		t.Errorf("expected ErrChromiumNotFound, got %v", err)
	}
}

func TestRender_PassesExpectedArgv(t *testing.T) {
	var gotName string
	var gotArgs []string
	stub := func(name string, args, _ []string) error {
		gotName = name
		gotArgs = args
		return nil
	}
	err := Render("/tmp/foo.html", "/tmp/foo.pdf", "/usr/bin/chromium", stub)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if gotName != "/usr/bin/chromium" {
		t.Errorf("expected chromium binary, got %q", gotName)
	}
	wantFlags := []string{
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--print-to-pdf=/tmp/foo.pdf",
		"file:///tmp/foo.html",
	}
	for _, want := range wantFlags {
		found := false
		for _, got := range gotArgs {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing argv %q in %v", want, gotArgs)
		}
	}
}

func TestRender_WrapsChromeError(t *testing.T) {
	stub := func(string, []string, []string) error {
		return errors.New("chrome exited 137")
	}
	err := Render("/tmp/foo.html", "/tmp/foo.pdf", "/usr/bin/chromium", stub)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "chrome exited 137") {
		t.Errorf("expected wrapped chrome error, got %q", err)
	}
	if !strings.Contains(err.Error(), "pdf:") {
		t.Errorf("expected pdf: prefix, got %q", err)
	}
}

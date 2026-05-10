package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPath_XDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got := Path()
	want := filepath.Join(dir, "md-preview", "config.toml")
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestPath_HomeFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	got := Path()
	if !strings.HasSuffix(got, filepath.Join(".config", "md-preview", "config.toml")) {
		t.Fatalf("Path() = %q, want suffix .config/md-preview/config.toml", got)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() err = %v, want nil", err)
	}
	if (cfg != Config{}) {
		t.Fatalf("Load() cfg = %+v, want zero", cfg)
	}
}

func TestEnsureDefault_CreatesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := EnsureDefault(); err != nil {
		t.Fatalf("EnsureDefault() err = %v, want nil", err)
	}
	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatalf("config not created: %v", err)
	}
	body := string(data)
	for _, want := range []string{"# theme", "# colemak", "# font_size"} {
		if !strings.Contains(body, want) {
			t.Errorf("seeded config missing %q", want)
		}
	}
	// Sanity: a fresh Load() over the seeded scaffold yields the zero Config
	// (everything is commented out).
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() over seeded config: %v", err)
	}
	if (cfg != Config{}) {
		t.Errorf("seeded config should parse as zero Config; got %+v", cfg)
	}
}

func TestEnsureDefault_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := []byte("theme = \"light\"\n")
	if err := os.WriteFile(Path(), original, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := EnsureDefault(); err != nil {
		t.Fatalf("EnsureDefault() err = %v, want nil", err)
	}
	got, err := os.ReadFile(Path())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("EnsureDefault clobbered existing config:\n got:  %q\n want: %q", got, original)
	}
}

func writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "md-preview", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestLoad_FullConfig(t *testing.T) {
	cases := []struct {
		name        string
		browserKey  string
		wantBrowser any
	}{
		{"string", `browser = "firefox --foo"`, "firefox --foo"},
		{"array", `browser = ["firefox", "-P", "default"]`, []any{"firefox", "-P", "default"}},
		{"auto", `browser = "auto"`, "auto"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "theme = \"light\"\nfont_size = 20\ncustom_css = \"~/foo.css\"\nedit = true\n" + tc.browserKey + "\n"
			writeConfig(t, body)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() err = %v", err)
			}
			if cfg.Theme != "light" {
				t.Errorf("Theme = %q, want light", cfg.Theme)
			}
			if cfg.FontSize == nil || *cfg.FontSize != 20 {
				t.Errorf("FontSize = %v, want 20", cfg.FontSize)
			}
			if cfg.CustomCSS != "~/foo.css" {
				t.Errorf("CustomCSS = %q", cfg.CustomCSS)
			}
			if !cfg.Edit {
				t.Errorf("Edit = false, want true")
			}
			switch want := tc.wantBrowser.(type) {
			case string:
				if got, ok := cfg.Browser.(string); !ok || got != want {
					t.Errorf("Browser = %#v, want %q", cfg.Browser, want)
				}
			case []any:
				got, ok := cfg.Browser.([]any)
				if !ok || len(got) != len(want) {
					t.Errorf("Browser = %#v, want %#v", cfg.Browser, want)
					return
				}
				for i, v := range want {
					if got[i] != v {
						t.Errorf("Browser[%d] = %v, want %v", i, got[i], v)
					}
				}
			}
		})
	}
}

func TestLoad_BadTOML(t *testing.T) {
	writeConfig(t, "this is = not = toml\n[[[\n")
	cfg, err := Load()
	if err == nil {
		t.Fatalf("Load() err = nil, want non-nil")
	}
	if (cfg != Config{}) {
		t.Fatalf("Load() cfg = %+v, want zero on error", cfg)
	}
}

func TestLoad_PartialConfig(t *testing.T) {
	writeConfig(t, "theme = \"dark\"\n")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if cfg.Theme != "dark" {
		t.Errorf("Theme = %q", cfg.Theme)
	}
	if cfg.FontSize != nil {
		t.Errorf("FontSize = %v, want nil", cfg.FontSize)
	}
	if cfg.CustomCSS != "" {
		t.Errorf("CustomCSS = %q, want empty", cfg.CustomCSS)
	}
	if cfg.Browser != nil {
		t.Errorf("Browser = %v, want nil", cfg.Browser)
	}
	if cfg.Edit {
		t.Errorf("Edit = true, want false")
	}
}

func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	cases := []struct {
		in, want string
	}{
		{"~/foo", filepath.Join(home, "foo")},
		{"/abs", "/abs"},
		{"relative", "relative"},
		{"~", "~"},
		{"", ""},
	}
	for _, c := range cases {
		if got := ExpandTilde(c.in); got != c.want {
			t.Errorf("ExpandTilde(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func ptrFloat(v float64) *float64 { return &v }

func TestExtraCSS_FontSize(t *testing.T) {
	var buf bytes.Buffer
	got := ExtraCSS(Config{FontSize: ptrFloat(20.0)}, &buf)
	if !strings.Contains(got, "body { font-size: 20px; }") {
		t.Errorf("output = %q, want body { font-size: 20px; }", got)
	}
	if buf.Len() != 0 {
		t.Errorf("errLog = %q, want empty", buf.String())
	}
}

func TestExtraCSS_CustomCSS(t *testing.T) {
	dir := t.TempDir()
	cssPath := filepath.Join(dir, "custom.css")
	contents := ".foo { color: red; }"
	if err := os.WriteFile(cssPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write css: %v", err)
	}

	t.Run("absolute", func(t *testing.T) {
		var buf bytes.Buffer
		got := ExtraCSS(Config{FontSize: ptrFloat(18), CustomCSS: cssPath}, &buf)
		if !strings.Contains(got, contents) {
			t.Errorf("output missing custom css: %q", got)
		}
		if !strings.Contains(got, "font-size") {
			t.Errorf("output missing font-size: %q", got)
		}
		if i := strings.Index(got, "font-size"); i == -1 || strings.Index(got, contents) <= i {
			t.Errorf("custom css should appear after font-size; got %q", got)
		}
		if buf.Len() != 0 {
			t.Errorf("errLog = %q", buf.String())
		}
	})

	t.Run("tilde", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		tildePath := filepath.Join(home, "tilde.css")
		body := "/* tilde */"
		if err := os.WriteFile(tildePath, []byte(body), 0o644); err != nil {
			t.Fatalf("write css: %v", err)
		}
		var buf bytes.Buffer
		got := ExtraCSS(Config{CustomCSS: "~/tilde.css"}, &buf)
		if !strings.Contains(got, body) {
			t.Errorf("output missing tilde css contents: %q", got)
		}
		if buf.Len() != 0 {
			t.Errorf("errLog = %q", buf.String())
		}
	})
}

func TestExtraCSS_BadFontSize(t *testing.T) {
	var buf bytes.Buffer
	got := ExtraCSS(Config{FontSize: ptrFloat(-1)}, &buf)
	if strings.Contains(got, "font-size") {
		t.Errorf("output should omit font-size for negative value; got %q", got)
	}
	if !strings.Contains(buf.String(), "config: invalid font_size") {
		t.Errorf("errLog = %q, want warning", buf.String())
	}
}

func TestExtraCSS_MissingCustomCSS(t *testing.T) {
	var buf bytes.Buffer
	missing := filepath.Join(t.TempDir(), "does-not-exist.css")
	got := ExtraCSS(Config{CustomCSS: missing}, &buf)
	if got != "" {
		t.Errorf("output = %q, want empty", got)
	}
	if !strings.Contains(buf.String(), "config: custom_css not found:") {
		t.Errorf("errLog = %q, want warning", buf.String())
	}
}

func TestExtraCSS_BothMissing(t *testing.T) {
	var buf bytes.Buffer
	got := ExtraCSS(Config{}, &buf)
	if got != "" {
		t.Errorf("output = %q, want empty", got)
	}
	if buf.Len() != 0 {
		t.Errorf("errLog = %q, want empty", buf.String())
	}
}

func fakeLookPath(found map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if p, ok := found[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
}

func TestBrowserCmd_Auto_Chrome(t *testing.T) {
	var buf bytes.Buffer
	lp := fakeLookPath(map[string]string{"google-chrome": "/usr/bin/google-chrome"})
	got := BrowserCmd(nil, "https://x", lp, "linux", &buf)
	want := []string{"/usr/bin/google-chrome", "--app=https://x"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBrowserCmd_Auto_Chromium(t *testing.T) {
	var buf bytes.Buffer
	lp := fakeLookPath(map[string]string{"chromium": "/usr/bin/chromium"})
	got := BrowserCmd(nil, "https://x", lp, "linux", &buf)
	want := []string{"/usr/bin/chromium", "--app=https://x"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBrowserCmd_Auto_NoneFound_Linux(t *testing.T) {
	var buf bytes.Buffer
	lp := fakeLookPath(nil)
	got := BrowserCmd(nil, "https://x", lp, "linux", &buf)
	want := []string{"xdg-open", "https://x"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBrowserCmd_Auto_NoneFound_Mac(t *testing.T) {
	var buf bytes.Buffer
	lp := fakeLookPath(nil)
	got := BrowserCmd(nil, "https://x", lp, "darwin", &buf)
	want := []string{"open", "https://x"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBrowserCmd_String(t *testing.T) {
	var buf bytes.Buffer
	got := BrowserCmd("firefox --foo bar", "https://x", nil, "linux", &buf)
	want := []string{"firefox", "--foo", "bar", "https://x"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBrowserCmd_Array(t *testing.T) {
	var buf bytes.Buffer
	got := BrowserCmd([]string{"firefox", "-P", "default"}, "https://x", nil, "linux", &buf)
	want := []string{"firefox", "-P", "default", "https://x"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBrowserCmd_AnyArray(t *testing.T) {
	var buf bytes.Buffer
	got := BrowserCmd([]any{"firefox", "-P", "default"}, "https://x", nil, "linux", &buf)
	want := []string{"firefox", "-P", "default", "https://x"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBrowserCmd_Invalid(t *testing.T) {
	var buf bytes.Buffer
	lp := fakeLookPath(map[string]string{"google-chrome": "/usr/bin/google-chrome"})
	got := BrowserCmd(42, "https://x", lp, "linux", &buf)
	want := []string{"/usr/bin/google-chrome", "--app=https://x"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if !strings.Contains(buf.String(), "config: invalid browser") {
		t.Errorf("errLog = %q, want warning", buf.String())
	}
}

func TestBrowserCmd_AutoExplicit(t *testing.T) {
	var buf bytes.Buffer
	lp := fakeLookPath(map[string]string{"google-chrome": "/usr/bin/google-chrome"})
	got := BrowserCmd("auto", "https://x", lp, "linux", &buf)
	want := []string{"/usr/bin/google-chrome", "--app=https://x"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	gotNil := BrowserCmd(nil, "https://x", lp, "linux", &buf)
	if !equalSlice(gotNil, got) {
		t.Errorf("nil and \"auto\" should match: %v vs %v", gotNil, got)
	}
}

func TestFzfPick_NotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := FzfPick(t.Context(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "fzf") {
		t.Fatalf("FzfPick err = %v, want error mentioning fzf", err)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

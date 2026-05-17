package render

import (
	"strings"
	"testing"
)

func keepBuild(prefix string) func(string) (string, bool) {
	return func(abs string) (string, bool) { return prefix + abs, true }
}

func TestRewriteImgSrc_RelativePath(t *testing.T) {
	in := `<p><img src="pic.png" alt="x"></p>`
	got := RewriteImgSrc(in, "/base", keepBuild("ABS:"))
	want := `<p><img src="ABS:/base/pic.png" alt="x"></p>`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteImgSrc_DotSlash(t *testing.T) {
	got := RewriteImgSrc(`<img src="./sub/pic.png">`, "/base", keepBuild(""))
	want := `<img src="/base/sub/pic.png">`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteImgSrc_AbsolutePath(t *testing.T) {
	got := RewriteImgSrc(`<img src="/etc/x.png">`, "/base", keepBuild(""))
	want := `<img src="/etc/x.png">`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteImgSrc_SkipsRemote(t *testing.T) {
	cases := []string{
		`<img src="https://example.com/x.png">`,
		`<img src="http://example.com/x.png">`,
		`<img src="data:image/png;base64,AAA">`,
		`<img src="file:///etc/passwd">`,
		`<img src="//cdn.example.com/x.png">`,
	}
	for _, in := range cases {
		got := RewriteImgSrc(in, "/base", keepBuild("REWROTE:"))
		if got != in {
			t.Errorf("rewrote remote src %q -> %q", in, got)
		}
	}
}

func TestRewriteImgSrc_StripsQueryFragment(t *testing.T) {
	got := RewriteImgSrc(`<img src="pic.png?v=2#frag">`, "/base", keepBuild(""))
	want := `<img src="/base/pic.png">`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteImgSrc_HTMLEntityDecoded(t *testing.T) {
	// Goldmark emits "a&amp;b.png" for a real filename "a&b.png";
	// resolveLocalSrc decodes the entity so the filesystem lookup
	// uses the real name, then the rewriter re-encodes the resulting
	// URL for HTML safety.
	got := RewriteImgSrc(`<img src="a&amp;b.png">`, "/base", keepBuild(""))
	want := `<img src="/base/a&amp;b.png">`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteImgSrc_PercentDecoded(t *testing.T) {
	got := RewriteImgSrc(`<img src="my%20pic.png">`, "/base", keepBuild(""))
	want := `<img src="/base/my pic.png">`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteImgSrc_MultipleImages(t *testing.T) {
	in := `<img src="a.png"><span>x</span><img src="b.png" alt="b">`
	got := RewriteImgSrc(in, "/base", keepBuild(""))
	want := `<img src="/base/a.png"><span>x</span><img src="/base/b.png" alt="b">`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteImgSrc_BuildNotOkKeepsOriginal(t *testing.T) {
	in := `<img src="pic.png">`
	got := RewriteImgSrc(in, "/base", func(abs string) (string, bool) { return "", false })
	if got != in {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestRewriteImgSrc_EscapesQuotesInBuildResult(t *testing.T) {
	got := RewriteImgSrc(`<img src="pic.png">`, "/base", func(abs string) (string, bool) { return `evil"injection`, true })
	if strings.Contains(got, `src="evil"`) {
		t.Errorf("unescaped quote leaked into attribute: %q", got)
	}
	if !strings.Contains(got, `&#34;`) && !strings.Contains(got, `&quot;`) {
		t.Errorf("expected an escaped quote (&#34; or &quot;) in output: %q", got)
	}
}

func TestFileURL_EncodesSpaces(t *testing.T) {
	got := FileURL("/home/user/my pic.png")
	want := "file:///home/user/my%20pic.png"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

package latex

import (
	"context"
	"strings"
	"testing"
)

func TestSanitize_StripsJavascriptHref(t *testing.T) {
	requirePandoc(t)
	src := []byte(`\href{javascript:alert(1)}{click}`)
	html, err := Render(context.Background(), src, Options{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(html, "javascript:") {
		t.Errorf("javascript: scheme survived sanitization:\n%s", html)
	}
}

func TestSanitize_StripsScriptTag(t *testing.T) {
	requirePandoc(t)
	src := []byte(`pre \href{http://example.com/<script>alert(1)</script>}{x} post`)
	html, err := Render(context.Background(), src, Options{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(strings.ToLower(html), "<script") {
		t.Errorf("<script> survived sanitization:\n%s", html)
	}
}

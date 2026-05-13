package pandoc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputFormat(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"foo.tex", "latex"},
		{"foo.latex", "latex"},
		{"FOO.TEX", "latex"},
		{"a/b/c.rst", "rst"},
		{"x.org", "org"},
		{"x.adoc", "asciidoc"},
		{"x.asciidoc", "asciidoc"},
		{"x.textile", "textile"},
		{"x.mediawiki", "mediawiki"},
		{"x.muse", "muse"},
		{"x.creole", "creole"},
		{"x.html", "html"},
		{"x.htm", "html"},
		{"x.typ", "typst"},
		{"x.opml", "opml"},
		{"x.t2t", "t2t"},
		{"x.rtf", "rtf"},
		{"x.bib", "biblatex"},
		{"x.ris", "ris"},
		{"x.csv", "csv"},
		{"x.tsv", "tsv"},
		{"x.dj", "djot"},
		{"x.djot", "djot"},
		{"x.jats", "jats"},
		{"x.ipynb", "ipynb"},
		{"x.dbk", "docbook"},
		{"x.docbook", "docbook"},
		{"x.docx", "docx"},
		{"x.odt", "odt"},
		{"x.epub", "epub"},
		{"x.fb2", "fb2"},
		{"x.pod", "pod"},
		{"x.pptx", "pptx"},
		{"x.xlsx", "xlsx"},
		{"x.man", "man"},
		{"x.mdoc", "mdoc"},
		{"x.csljson", "csljson"},
		{"x.jira", "jira"},
		{"x.vimwiki", "vimwiki"},
		{"x.dokuwiki", "dokuwiki"},
		{"x.haddock", "haddock"},
		{"x.tikiwiki", "tikiwiki"},
		{"x.twiki", "twiki"},
		{"x.bits", "bits"},
		{"x.md", ""},
		{"x.markdown", ""},
		{"x.txt", ""},
		{"x", ""},
		{"x.unknown", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := InputFormat(tt.path); got != tt.want {
				t.Errorf("InputFormat(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// pandocInputFormats lists what the *resolved* pandoc claims it can
// read — same binary Render will shell out to. We skip-with-reason
// for formats that landed in newer releases (asciidoc-input in 3.6,
// djot in 3.5, pptx/xlsx-input in 3.5, etc.) when CI runs against an
// older pandoc.
func pandocInputFormats(t *testing.T) map[string]bool {
	t.Helper()
	if !Available() {
		t.Skip("pandoc not available")
	}
	out, err := exec.Command(binPath, "--list-input-formats").Output()
	if err != nil {
		t.Skipf("%s --list-input-formats: %v", binPath, err)
	}
	set := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		set[strings.TrimSpace(line)] = true
	}
	return set
}

func testdataFormats(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..", "..", "testdata", "formats")
}

// TestRender_AllFormats drives each extension-detectable pandoc input
// format through Render and asserts an expected HTML substring shows
// up. Text fixtures are inline; binary fixtures live under
// testdata/formats/. Cases whose format isn't in the local pandoc's
// input list skip with a reason so CI on older pandoc still passes.
func TestRender_AllFormats(t *testing.T) {
	if !Available() {
		t.Skip("pandoc not on PATH")
	}
	supported := pandocInputFormats(t)
	fixtureDir := testdataFormats(t)

	cases := []struct {
		name string
		ext  string
		// Exactly one of src (inline text) or binFile (relative path
		// under testdata/formats/) is set.
		src     string
		binFile string
		// Substrings every pandoc-rendered output must contain.
		want []string
	}{
		{
			name: "latex",
			ext:  ".tex",
			src:  `\textbf{bold}`,
			want: []string{"<strong>bold</strong>"},
		},
		{
			name: "rst",
			ext:  ".rst",
			src:  "**bold** text\n",
			want: []string{"<strong>bold</strong>"},
		},
		{
			name: "org",
			ext:  ".org",
			src:  "*bold* text\n",
			want: []string{"bold"},
		},
		{
			name: "asciidoc",
			ext:  ".adoc",
			src:  "*bold* text\n",
			want: []string{"bold"},
		},
		{
			name: "textile",
			ext:  ".textile",
			src:  "*bold* text\n",
			want: []string{"bold"},
		},
		{
			name: "mediawiki",
			ext:  ".mediawiki",
			src:  "'''bold''' text\n",
			want: []string{"bold"},
		},
		{
			name: "muse",
			ext:  ".muse",
			src:  "**bold** text\n",
			want: []string{"bold"},
		},
		{
			name: "creole",
			ext:  ".creole",
			src:  "**bold** text\n",
			want: []string{"bold"},
		},
		{
			name: "html",
			ext:  ".html",
			src:  "<p><strong>bold</strong> text</p>",
			want: []string{"<strong>bold</strong>"},
		},
		{
			name: "typst",
			ext:  ".typ",
			src:  "*bold* text\n",
			want: []string{"bold"},
		},
		{
			name: "opml",
			ext:  ".opml",
			src: `<?xml version="1.0"?>
<opml version="2.0">
<head><title>Outline</title></head>
<body><outline text="Item one"/></body>
</opml>`,
			want: []string{"Item one"},
		},
		{
			name: "t2t",
			ext:  ".t2t",
			src:  "**bold** text\n",
			want: []string{"bold"},
		},
		{
			name: "biblatex",
			ext:  ".bib",
			src:  "@book{k, title = {My Book}, author = {Smith, Jane}, year = {2020}}\n",
			want: []string{"Book"},
		},
		{
			name: "ris",
			ext:  ".ris",
			src:  "TY  - JOUR\nT1  - Sample Title\nAU  - Doe, John\nPY  - 2020\nER  - \n",
			want: []string{"Sample Title"},
		},
		{
			name: "csv",
			ext:  ".csv",
			src:  "Name,Value\nalpha,1\nbeta,2\n",
			want: []string{"<table", "alpha"},
		},
		{
			name: "tsv",
			ext:  ".tsv",
			src:  "Name\tValue\nalpha\t1\nbeta\t2\n",
			want: []string{"<table", "alpha"},
		},
		{
			name: "djot",
			ext:  ".dj",
			src:  "_emph_ text\n",
			want: []string{"emph"},
		},
		{
			name: "jats",
			ext:  ".jats",
			src: `<?xml version="1.0" encoding="UTF-8"?>
<article>
<front><article-meta><title-group><article-title>Hello</article-title></title-group></article-meta></front>
<body><p>The <bold>bold</bold> text.</p></body>
</article>`,
			want: []string{"bold"},
		},
		{
			name: "ipynb",
			ext:  ".ipynb",
			src: `{
  "cells": [
    {"cell_type": "markdown", "metadata": {}, "source": ["# Hello\n", "\n", "Body text.\n"]}
  ],
  "metadata": {"kernelspec": {"display_name": "Python 3", "language": "python", "name": "python3"}},
  "nbformat": 4,
  "nbformat_minor": 5
}`,
			want: []string{"Hello"},
		},
		{
			name: "docbook",
			ext:  ".dbk",
			src: `<?xml version="1.0"?>
<article xmlns="http://docbook.org/ns/docbook" version="5.0">
<title>Hello</title>
<para>The <emphasis role="strong">bold</emphasis> text.</para>
</article>`,
			want: []string{"bold"},
		},
		{
			name: "csljson",
			ext:  ".csljson",
			src:  `[{"id":"k","type":"book","title":"Hello","author":[{"family":"Smith","given":"J"}]}]`,
			want: []string{"Hello"},
		},
		{
			name: "jira",
			ext:  ".jira",
			src:  "h1. Hello\n\n*bold* text\n",
			want: []string{"<h1>Hello</h1>", "<strong>bold</strong>"},
		},
		{
			name: "vimwiki",
			ext:  ".vimwiki",
			src:  "= Hello =\n\n*bold* text\n",
			want: []string{"Hello", "<strong>bold</strong>"},
		},
		{
			name: "dokuwiki",
			ext:  ".dokuwiki",
			src:  "====== Hello ======\n\n**bold**\n",
			want: []string{"Hello", "<strong>bold</strong>"},
		},
		{
			name: "man",
			ext:  ".man",
			src:  ".TH HELLO 1\n.SH NAME\nhello \\- demo\n",
			want: []string{"NAME", "hello"},
		},
		{
			name: "mdoc",
			ext:  ".mdoc",
			src:  ".Dd $Mdocdate$\n.Dt HELLO 1\n.Os\n.Sh NAME\n.Nm hello\n.Nd demo\n",
			want: []string{"NAME", "hello"},
		},
		{
			name: "pod",
			ext:  ".pod",
			src:  "=head1 Hello\n\nThis is B<bold> text.\n\n=cut\n",
			want: []string{"Hello", "<strong>bold</strong>"},
		},
		// Binary fixtures committed under testdata/formats/, all
		// regenerated from fixture-source.md (except sample.xlsx which
		// comes from sample.xlsx-source.csv since pandoc can't write
		// xlsx).
		{
			name: "docx",
			ext:  ".docx",
			binFile: "sample.docx",
			want: []string{"Sample document", "<strong>bold</strong>"},
		},
		{
			name: "odt",
			ext:  ".odt",
			binFile: "sample.odt",
			want: []string{"Sample document", "<strong>bold</strong>"},
		},
		{
			name: "epub",
			ext:  ".epub",
			binFile: "sample.epub",
			want: []string{"Sample document"},
		},
		{
			name: "fb2",
			ext:  ".fb2",
			binFile: "sample.fb2",
			want: []string{"Sample document"},
		},
		{
			name: "pptx",
			ext:  ".pptx",
			binFile: "sample.pptx",
			want: []string{"Sample document"},
		},
		{
			name: "xlsx",
			ext:  ".xlsx",
			binFile: "sample.xlsx",
			want: []string{"<table", "docx", "binary"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			format := InputFormat("x" + tc.ext)
			if format == "" {
				t.Fatalf("InputFormat(%q) returned empty; missing dispatch entry?", tc.ext)
			}
			if !supported[format] {
				t.Skipf("pandoc %q does not list %q as an input format", binPath, format)
			}
			var src []byte
			if tc.binFile != "" {
				var err error
				src, err = os.ReadFile(filepath.Join(fixtureDir, tc.binFile))
				if err != nil {
					t.Fatalf("read fixture %s: %v", tc.binFile, err)
				}
			} else {
				src = []byte(tc.src)
			}
			got, err := Render(context.Background(), src, "", format)
			if err != nil {
				t.Fatalf("Render(%s): %v\nsrc:\n%s", format, err, string(src))
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("Render(%s) missing %q\noutput: %s", format, want, got)
				}
			}
		})
	}
}

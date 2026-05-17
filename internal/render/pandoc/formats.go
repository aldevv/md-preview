package pandoc

import (
	"path/filepath"
	"strings"
)

// markdown is intentionally absent (mdp routes .md through goldmark).
// Plain-text-flavor formats that need a --from flag to disambiguate
// (gfm, commonmark, commonmark_x, markdown_strict, markdown_mmd,
// markdown_phpextra, markdown_github, native, json, xml) are also
// excluded; the extension alone can't pick the right flavor.
var pandocExtFormat = map[string]string{
	".tex":       "latex",
	".latex":     "latex",
	".rst":       "rst",
	".org":       "org",
	".adoc":      "asciidoc",
	".asciidoc":  "asciidoc",
	".textile":   "textile",
	".mediawiki": "mediawiki",
	".muse":      "muse",
	".creole":    "creole",
	".html":      "html",
	".htm":       "html",
	".typ":       "typst",
	".opml":      "opml",
	".t2t":       "t2t",
	".rtf":       "rtf",
	".bib":       "biblatex",
	".ris":       "ris",
	".csljson":   "csljson",
	".csv":       "csv",
	".tsv":       "tsv",
	".dj":        "djot",
	".djot":      "djot",
	".jats":      "jats",
	".ipynb":     "ipynb",
	".docbook":   "docbook",
	".dbk":       "docbook",
	".docx":      "docx",
	".odt":       "odt",
	".epub":      "epub",
	".fb2":       "fb2",
	".pod":       "pod",
	".pptx":      "pptx",
	".xlsx":      "xlsx",
	".man":       "man",
	".mdoc":      "mdoc",
	".jira":      "jira",
	".vimwiki":   "vimwiki",
	".dokuwiki":  "dokuwiki",
	".haddock":   "haddock",
	".tikiwiki":  "tikiwiki",
	".twiki":     "twiki",
	".bits":      "bits",
}

func InputFormat(path string) string {
	return pandocExtFormat[strings.ToLower(filepath.Ext(path))]
}

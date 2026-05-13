package pandoc

import (
	"path/filepath"
	"strings"
)

// pandocExtFormat maps a lowercase file extension (with leading dot)
// to the pandoc --from name. Only extensions that unambiguously
// identify a pandoc input format are listed; markdown is intentionally
// absent (mdp routes .md through goldmark, not pandoc).
//
// Formats whose only "extension" is a flavor of plain text and so
// need a --from CLI flag to disambiguate (gfm, commonmark,
// commonmark_x, markdown_strict, markdown_mmd, markdown_phpextra,
// markdown_github, native, json, xml) are not in this table.
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

// InputFormat returns the pandoc --from name for path's extension, or
// "" if the extension isn't a recognized pandoc input format. Callers
// use the empty return to fall back to goldmark (for .md) or to reject
// the file.
func InputFormat(path string) string {
	return pandocExtFormat[strings.ToLower(filepath.Ext(path))]
}

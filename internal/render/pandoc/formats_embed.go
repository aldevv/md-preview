package pandoc

import (
	_ "embed"
	"strings"
)

// supportedFormatsRaw is the `pandoc --list-input-formats` output for
// the pinned auto-fetch version (see Version in fetch.go). The
// pandoc-bump GitHub workflow regenerates this file daily against the
// latest upstream release; manual edits get overwritten.
//
//go:embed supported_formats.txt
var supportedFormatsRaw string

var supportedByPinned = func() map[string]bool {
	set := make(map[string]bool, 64)
	for _, line := range strings.Split(supportedFormatsRaw, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			set[name] = true
		}
	}
	return set
}()

// PinnedSupports reports whether the pinned auto-fetch pandoc version
// can read the given format (from `pandoc --list-input-formats`).
// Callers use it to decide whether auto-fetching would actually help
// when the host pandoc doesn't list a needed format.
func PinnedSupports(format string) bool {
	return supportedByPinned[format]
}

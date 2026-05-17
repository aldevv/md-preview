package pandoc

import (
	_ "embed"
	"strings"
)

// Regenerated daily by the pandoc-bump workflow; manual edits get
// overwritten.
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

func PinnedSupports(format string) bool {
	return supportedByPinned[format]
}

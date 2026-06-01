// Package pdf renders an HTML file to PDF by shelling out to a
// Chromium-family browser in headless mode. The HTML file is read via
// file:// so the mdp HTTP server doesn't need to be running.
package pdf

import (
	"errors"
	"fmt"
)

// ErrChromiumNotFound is returned when no Chromium-family binary can be
// resolved. Callers should surface an "install Chromium" hint.
var ErrChromiumNotFound = errors.New("pdf: chromium-family browser not found")

// RunFunc matches Environment.RunCmd: synchronous exec inheriting
// stdout/stderr. Injected so tests don't spawn a real browser.
type RunFunc func(name string, args []string, environ []string) error

// Render writes a PDF of htmlPath to outPath using chromiumBin. chromiumBin
// must be a Chromium-family binary path (resolve via config.ChromiumPath).
// outPath is created or overwritten by Chrome; we don't touch the bytes.
func Render(htmlPath, outPath, chromiumBin string, run RunFunc) error {
	if chromiumBin == "" {
		return ErrChromiumNotFound
	}
	argv := []string{
		"--headless",
		"--disable-gpu",
		// --no-sandbox lets Chromium run as root in containers and as
		// regular user in environments without user-namespaces (some
		// Docker setups, minimal Linux distros). Same trade modern CI
		// pipelines make for headless print pipelines.
		"--no-sandbox",
		"--print-to-pdf=" + outPath,
		"file://" + htmlPath,
	}
	if err := run(chromiumBin, argv, nil); err != nil {
		return fmt.Errorf("pdf: chromium exit: %w", err)
	}
	return nil
}

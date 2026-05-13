// Package nativewin opens a native chromeless window pointed at a URL,
// without spawning the user's browser. On linux it dlopens WebKitGTK via
// purego (no CGO needed at compile time). On darwin it drives Cocoa
// WKWebView via purego/objc (Objective-C runtime calls, also no CGO).
// Unsupported platforms return ErrUnsupported and the caller should
// fall back to spawning the user's browser.
//
// The implementation is split into per-OS files. nativewin_linux.go and
// nativewin_darwin.go each define Open and Available; this file holds the
// shared Options struct, the sentinel error, and package-level docs.
package nativewin

import "errors"

// ErrUnsupported is returned by Open when the current platform has no
// native-window backend wired up, or when the required runtime libraries
// are missing (e.g. libwebkit2gtk-4.1 not installed on Linux).
var ErrUnsupported = errors.New("nativewin: unsupported platform or missing runtime libraries")

// Options configures the native window. Width/Height of 0 use the
// per-platform default.
//
// AssetsDir, when non-empty, registers an mdp:// URI scheme handler
// that serves files from this directory with proper Content-Type
// headers. Use with URL = "mdp:///<file>" so the file:// MIME quirk
// in WebKitGTK doesn't block WebAssembly.compileStreaming.
type Options struct {
	URL       string
	Title     string
	AssetsDir string
	Width     int
	Height    int
}

func (o Options) titleOrDefault() string {
	if o.Title == "" {
		return "mdp"
	}
	return o.Title
}

func (o Options) widthOrDefault() int {
	if o.Width <= 0 {
		return 1024
	}
	return o.Width
}

func (o Options) heightOrDefault() int {
	if o.Height <= 0 {
		return 768
	}
	return o.Height
}

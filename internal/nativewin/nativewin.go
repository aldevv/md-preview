// Package nativewin opens a chromeless OS window at a URL without
// spawning the user's browser. Linux dlopens WebKitGTK via purego;
// darwin drives WKWebView via purego/objc. Both keep CGO disabled.
// Unsupported platforms return ErrUnsupported so the caller can fall
// back to a browser spawn.
package nativewin

import "errors"

// ErrUnsupported signals that no backend is available, either because
// the OS isn't supported or because the runtime libs (e.g.
// libwebkit2gtk-4.1) aren't installed.
var ErrUnsupported = errors.New("nativewin: unsupported platform or missing runtime libraries")

// Options configures the native window. Width/Height of 0 use the
// per-platform default.
type Options struct {
	URL    string
	Title  string
	Width  int
	Height int
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

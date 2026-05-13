//go:build linux

package nativewin

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// GTK + WebKitGTK, accessed at runtime via purego so the Go binary
// stays CGO_ENABLED=0. The .so files we need:
//
//   libgtk-3.so.0           GTK 3 toolkit
//   libgobject-2.0.so.0     GLib's GObject runtime (signal connection)
//   libwebkit2gtk-4.1.so.0  WebKit2GTK (libsoup3) — modern distros
//   libwebkit2gtk-4.0.so.0  WebKit2GTK (libsoup2) — Ubuntu 22.04 LTS,
//                           Debian 11, RHEL 9, etc.
//
// We probe -4.1 first (newer) and fall back to -4.0. The C API for the
// symbols we use (webkit_web_view_new, webkit_web_view_load_uri) is
// identical between the two. Distros without either won't have the
// native path: Available() returns false and the caller falls back to
// spawning the user's browser.

const (
	gtkWindowToplevel = 0
)

var (
	loadOnce sync.Once
	loadErr  error

	gtkInitCheck         func(argc *int32, argv unsafe.Pointer) int32
	gtkWindowNew         func(typ int32) uintptr
	gtkWindowSetTitle    func(window uintptr, title string)
	gtkWindowSetDefSize  func(window uintptr, w, h int32)
	gtkContainerAdd      func(container, widget uintptr)
	gtkWidgetShowAll     func(widget uintptr)
	gtkMain              func()
	gtkMainQuit          func()
	webkitWebViewNew     func() uintptr
	webkitWebViewLoadURI func(view uintptr, uri string)
	gSignalConnectData   func(instance uintptr, signal string, handler uintptr, data uintptr, destroyData uintptr, flags int32) uint64

	availOnce sync.Once
	availOK   bool
)

// dlopenWebKit tries -4.1 first, then -4.0. Returns the lib handle and
// the soname we found, or an error.
func dlopenWebKit(mode int) (uintptr, string, error) {
	for _, name := range []string{
		"libwebkit2gtk-4.1.so.0",
		"libwebkit2gtk-4.0.so.0",
	} {
		if h, err := purego.Dlopen(name, mode); err == nil {
			return h, name, nil
		}
	}
	return 0, "", fmt.Errorf("dlopen libwebkit2gtk: neither -4.1 nor -4.0 available")
}

// dlopenAll opens the three shared libraries (with RTLD_GLOBAL so
// WebKit's plugin loader can resolve dependent symbols at runtime) and
// binds the symbols we use.
func dlopenAll() error {
	mode := purego.RTLD_NOW | purego.RTLD_GLOBAL
	libgobject, err := purego.Dlopen("libgobject-2.0.so.0", mode)
	if err != nil {
		return fmt.Errorf("dlopen libgobject-2.0: %w", err)
	}
	libgtk, err := purego.Dlopen("libgtk-3.so.0", mode)
	if err != nil {
		return fmt.Errorf("dlopen libgtk-3: %w", err)
	}
	libwebkit, soname, err := dlopenWebKit(mode)
	if err != nil {
		return err
	}
	_ = soname // available for future logging if needed

	purego.RegisterLibFunc(&gtkInitCheck, libgtk, "gtk_init_check")
	purego.RegisterLibFunc(&gtkWindowNew, libgtk, "gtk_window_new")
	purego.RegisterLibFunc(&gtkWindowSetTitle, libgtk, "gtk_window_set_title")
	purego.RegisterLibFunc(&gtkWindowSetDefSize, libgtk, "gtk_window_set_default_size")
	purego.RegisterLibFunc(&gtkContainerAdd, libgtk, "gtk_container_add")
	purego.RegisterLibFunc(&gtkWidgetShowAll, libgtk, "gtk_widget_show_all")
	purego.RegisterLibFunc(&gtkMain, libgtk, "gtk_main")
	purego.RegisterLibFunc(&gtkMainQuit, libgtk, "gtk_main_quit")
	purego.RegisterLibFunc(&webkitWebViewNew, libwebkit, "webkit_web_view_new")
	purego.RegisterLibFunc(&webkitWebViewLoadURI, libwebkit, "webkit_web_view_load_uri")
	purego.RegisterLibFunc(&gSignalConnectData, libgobject, "g_signal_connect_data")
	return nil
}

// Available reports whether the runtime libraries are present. Uses a
// non-polluting probe (RTLD_LAZY|RTLD_LOCAL, immediately Dlclose'd) and
// caches the result so repeat calls are cheap.
func Available() bool {
	availOnce.Do(func() {
		probeMode := purego.RTLD_LAZY | purego.RTLD_LOCAL
		for _, name := range []string{"libgobject-2.0.so.0", "libgtk-3.so.0"} {
			h, err := purego.Dlopen(name, probeMode)
			if err != nil {
				return
			}
			_ = purego.Dlclose(h)
		}
		h, _, err := dlopenWebKit(probeMode)
		if err != nil {
			return
		}
		_ = purego.Dlclose(h)
		availOK = true
	})
	return availOK
}

// Open creates a GtkWindow containing a WebKitWebView pointed at
// opts.URL, then blocks on gtk_main() until the user closes the window.
// Returns ErrUnsupported if the runtime libraries are missing.
//
// GTK is not thread-safe and requires init + main loop on the same OS
// thread, so we lock the goroutine to its OS thread for the duration.
func Open(opts Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	loadOnce.Do(func() { loadErr = dlopenAll() })
	if loadErr != nil {
		return ErrUnsupported
	}

	var argc int32 = 0
	if gtkInitCheck(&argc, nil) == 0 {
		return fmt.Errorf("nativewin: gtk_init_check failed (no display?)")
	}

	window := gtkWindowNew(gtkWindowToplevel)
	gtkWindowSetTitle(window, opts.titleOrDefault())
	gtkWindowSetDefSize(window, int32(opts.widthOrDefault()), int32(opts.heightOrDefault()))

	view := webkitWebViewNew()
	gtkContainerAdd(window, view)
	webkitWebViewLoadURI(view, opts.URL)

	// "destroy" fires after the user clicks close; quit the main loop so
	// Open() returns and the caller can shut down the server.
	destroyCB := purego.NewCallback(func(_ uintptr, _ uintptr) uintptr {
		gtkMainQuit()
		return 0
	})
	gSignalConnectData(window, "destroy", destroyCB, 0, 0, 0)

	gtkWidgetShowAll(window)
	gtkMain()
	return nil
}

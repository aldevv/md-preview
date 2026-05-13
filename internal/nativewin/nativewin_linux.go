//go:build linux

package nativewin

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	gtkWindowToplevel                = 0
	webkitHardwareAccelerationAlways = 0
	webkitCacheModelWebBrowser       = 2
)

var (
	loadOnce sync.Once
	loadErr  error

	gtkInitCheck                                func(argc *int32, argv unsafe.Pointer) int32
	gtkWindowNew                                func(typ int32) uintptr
	gtkWindowSetTitle                           func(window uintptr, title string)
	gtkWindowSetDefSize                         func(window uintptr, w, h int32)
	gtkContainerAdd                             func(container, widget uintptr)
	gtkWidgetShowAll                            func(widget uintptr)
	gtkMain                                     func()
	gtkMainQuit                                 func()
	webkitWebViewNew                            func() uintptr
	webkitWebViewLoadURI                        func(view uintptr, uri string)
	webkitWebViewGetSettings                    func(view uintptr) uintptr
	webkitWebViewGetContext                     func(view uintptr) uintptr
	webkitSettingsSetHardwareAccelerationPolicy func(settings uintptr, policy int32)
	webkitSettingsSetEnableSmoothScrolling      func(settings uintptr, enabled int32)
	webkitWebContextSetCacheModel               func(ctx uintptr, model int32)
	gSignalConnectData                          func(instance uintptr, signal string, handler uintptr, data uintptr, destroyData uintptr, flags int32) uint64

	availOnce sync.Once
	availOK   bool
)

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
	libwebkit, _, err := dlopenWebKit(mode)
	if err != nil {
		return err
	}

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
	purego.RegisterLibFunc(&webkitWebViewGetSettings, libwebkit, "webkit_web_view_get_settings")
	purego.RegisterLibFunc(&webkitWebViewGetContext, libwebkit, "webkit_web_view_get_context")
	purego.RegisterLibFunc(&webkitSettingsSetHardwareAccelerationPolicy, libwebkit, "webkit_settings_set_hardware_acceleration_policy")
	purego.RegisterLibFunc(&webkitSettingsSetEnableSmoothScrolling, libwebkit, "webkit_settings_set_enable_smooth_scrolling")
	purego.RegisterLibFunc(&webkitWebContextSetCacheModel, libwebkit, "webkit_web_context_set_cache_model")
	purego.RegisterLibFunc(&gSignalConnectData, libgobject, "g_signal_connect_data")
	return nil
}

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

// Open creates a GtkWindow + WebKitWebView pointed at opts.URL, then
// blocks on gtk_main until the user closes the window.
func Open(opts Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Faster JIT tier-up for the wasm cold path.
	_ = os.Setenv("JSC_jitPolicyScale", "0.1")

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

	settings := webkitWebViewGetSettings(view)
	webkitSettingsSetHardwareAccelerationPolicy(settings, webkitHardwareAccelerationAlways)
	webkitSettingsSetEnableSmoothScrolling(settings, 0)

	webkitWebContextSetCacheModel(webkitWebViewGetContext(view), webkitCacheModelWebBrowser)

	webkitWebViewLoadURI(view, opts.URL)

	destroyCB := purego.NewCallback(func(_ uintptr, _ uintptr) uintptr {
		gtkMainQuit()
		return 0
	})
	gSignalConnectData(window, "destroy", destroyCB, 0, 0, 0)

	gtkWidgetShowAll(window)
	gtkMain()
	return nil
}

//go:build linux

package nativewin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

	gtkInitCheck                                   func(argc *int32, argv unsafe.Pointer) int32
	gtkWindowNew                                   func(typ int32) uintptr
	gtkWindowSetTitle                              func(window uintptr, title string)
	gtkWindowSetDefSize                            func(window uintptr, w, h int32)
	gtkContainerAdd                                func(container, widget uintptr)
	gtkWidgetShowAll                               func(widget uintptr)
	gtkMain                                        func()
	gtkMainQuit                                    func()
	webkitWebViewNew                               func() uintptr
	webkitWebViewLoadURI                           func(view uintptr, uri string)
	webkitWebViewGetSettings                       func(view uintptr) uintptr
	webkitWebViewGetContext                        func(view uintptr) uintptr
	webkitSettingsSetAllowFileAccessFromFileURL    func(settings uintptr, allowed int32)
	webkitSettingsSetHardwareAccelerationPolicy    func(settings uintptr, policy int32)
	webkitSettingsSetEnableSmoothScrolling         func(settings uintptr, enabled int32)
	webkitWebContextSetCacheModel                  func(ctx uintptr, model int32)
	webkitWebContextRegisterURIScheme              func(ctx uintptr, scheme string, cb uintptr, userData uintptr, destroyData uintptr)
	webkitWebContextGetSecurityManager             func(ctx uintptr) uintptr
	webkitSecurityManagerRegisterURISchemeAsSecure func(sm uintptr, scheme string)
	webkitSecurityManagerRegisterURISchemeAsCORS   func(sm uintptr, scheme string)
	webkitURISchemeRequestGetPath                  func(req uintptr) *byte
	webkitURISchemeRequestFinish                   func(req uintptr, stream uintptr, length int64, contentType string)
	webkitURISchemeRequestFinishError              func(req uintptr, err uintptr)
	gMemoryInputStreamNewFromData                  func(data unsafe.Pointer, length int64, destroy uintptr) uintptr
	gObjectUnref                                   func(obj uintptr)
	gSignalConnectData                             func(instance uintptr, signal string, handler uintptr, data uintptr, destroyData uintptr, flags int32) uint64

	availOnce sync.Once
	availOK   bool

	schemeAssetsDir string
	schemeBufs      sync.Map // path -> *[]byte (pinned forever; cheap)
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
	libgio, err := purego.Dlopen("libgio-2.0.so.0", mode)
	if err != nil {
		return fmt.Errorf("dlopen libgio-2.0: %w", err)
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
	purego.RegisterLibFunc(&webkitSettingsSetAllowFileAccessFromFileURL, libwebkit, "webkit_settings_set_allow_file_access_from_file_urls")
	purego.RegisterLibFunc(&webkitSettingsSetHardwareAccelerationPolicy, libwebkit, "webkit_settings_set_hardware_acceleration_policy")
	purego.RegisterLibFunc(&webkitSettingsSetEnableSmoothScrolling, libwebkit, "webkit_settings_set_enable_smooth_scrolling")
	purego.RegisterLibFunc(&webkitWebContextSetCacheModel, libwebkit, "webkit_web_context_set_cache_model")
	purego.RegisterLibFunc(&webkitWebContextRegisterURIScheme, libwebkit, "webkit_web_context_register_uri_scheme")
	purego.RegisterLibFunc(&webkitWebContextGetSecurityManager, libwebkit, "webkit_web_context_get_security_manager")
	purego.RegisterLibFunc(&webkitSecurityManagerRegisterURISchemeAsSecure, libwebkit, "webkit_security_manager_register_uri_scheme_as_secure")
	purego.RegisterLibFunc(&webkitSecurityManagerRegisterURISchemeAsCORS, libwebkit, "webkit_security_manager_register_uri_scheme_as_cors_enabled")
	purego.RegisterLibFunc(&webkitURISchemeRequestGetPath, libwebkit, "webkit_uri_scheme_request_get_path")
	purego.RegisterLibFunc(&webkitURISchemeRequestFinish, libwebkit, "webkit_uri_scheme_request_finish")
	purego.RegisterLibFunc(&webkitURISchemeRequestFinishError, libwebkit, "webkit_uri_scheme_request_finish_error")
	purego.RegisterLibFunc(&gMemoryInputStreamNewFromData, libgio, "g_memory_input_stream_new_from_data")
	purego.RegisterLibFunc(&gObjectUnref, libgobject, "g_object_unref")
	purego.RegisterLibFunc(&gSignalConnectData, libgobject, "g_signal_connect_data")
	return nil
}

func Available() bool {
	availOnce.Do(func() {
		probeMode := purego.RTLD_LAZY | purego.RTLD_LOCAL
		for _, name := range []string{"libgobject-2.0.so.0", "libgio-2.0.so.0", "libgtk-3.so.0"} {
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

// Open creates a GtkWindow + WebKitWebView, optionally registers the
// mdp:// scheme handler when opts.AssetsDir is set, then blocks on
// gtk_main until the user closes the window.
func Open(opts Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Faster JIT tier-up for the wasm cold path; harmless when WebKit
	// is already loaded since the env is read once at JSC init.
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
	webkitSettingsSetAllowFileAccessFromFileURL(settings, 1)
	webkitSettingsSetHardwareAccelerationPolicy(settings, webkitHardwareAccelerationAlways)
	webkitSettingsSetEnableSmoothScrolling(settings, 0)

	ctx := webkitWebViewGetContext(view)
	webkitWebContextSetCacheModel(ctx, webkitCacheModelWebBrowser)

	if opts.AssetsDir != "" {
		registerMdpScheme(ctx, opts.AssetsDir)
	}

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

// registerMdpScheme wires the mdp:// scheme to serve files from dir
// with proper Content-Type. Marked secure + CORS so IndexedDB and
// streaming compile work the same as on http://.
func registerMdpScheme(ctx uintptr, dir string) {
	schemeAssetsDir = dir
	cb := purego.NewCallback(handleMdpRequest)
	webkitWebContextRegisterURIScheme(ctx, "mdp", cb, 0, 0)
	sm := webkitWebContextGetSecurityManager(ctx)
	webkitSecurityManagerRegisterURISchemeAsSecure(sm, "mdp")
	webkitSecurityManagerRegisterURISchemeAsCORS(sm, "mdp")
}

func handleMdpRequest(request uintptr, _ uintptr) uintptr {
	pathPtr := webkitURISchemeRequestGetPath(request)
	rel := strings.TrimPrefix(cstring(pathPtr), "/")
	if rel == "" || strings.Contains(rel, "..") {
		webkitURISchemeRequestFinishError(request, 0)
		return 0
	}
	var data []byte
	if v, ok := schemeBufs.Load(rel); ok {
		data = *v.(*[]byte)
	} else {
		d, err := os.ReadFile(filepath.Join(schemeAssetsDir, rel))
		if err != nil {
			webkitURISchemeRequestFinishError(request, 0)
			return 0
		}
		data = d
		schemeBufs.Store(rel, &data)
	}
	stream := gMemoryInputStreamNewFromData(unsafe.Pointer(&data[0]), int64(len(data)), 0)
	webkitURISchemeRequestFinish(request, stream, int64(len(data)), mimeFor(rel))
	gObjectUnref(stream)
	return 0
}

func mimeFor(name string) string {
	switch {
	case strings.HasSuffix(name, ".wasm"):
		return "application/wasm"
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

// cstring copies a NUL-terminated C string into a Go string.
func cstring(p *byte) string {
	if p == nil {
		return ""
	}
	const maxLen = 1 << 20
	bs := unsafe.Slice(p, maxLen)
	for i, b := range bs {
		if b == 0 {
			return string(bs[:i])
		}
	}
	return string(bs)
}

//go:build linux

package nativewin

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	gtkWindowToplevel = 0
	// WebKitHardwareAccelerationPolicy. ALWAYS forces GPU compositor
	// setup on every Open(); on a cold start that adds ~300-500ms to
	// first-contentful-paint per measurements. NEVER uses the software
	// renderer which is plenty fast for our markdown viewport and
	// gets text on screen sooner.
	webkitHardwareAccelerationAlways = 0
	webkitHardwareAccelerationNever  = 1
	webkitCacheModelWebBrowser       = 2
	// WebKitLoadEvent enum (webkit2/WebKitLoadEvent).
	webkitLoadStarted    = 0
	webkitLoadRedirected = 1
	webkitLoadCommitted  = 2
	webkitLoadFinished   = 3
)

// init pre-warms the dlopen so the first user-facing Open() doesn't
// pay the ~50-200ms libwebkit2gtk + libgtk + libgobject load cost on
// the hot path. Skipped when the user opted out of the native window;
// in that case Open() is never reached anyway.
func init() {
	if v := os.Getenv("MDP_NATIVE"); v == "0" || v == "false" {
		return
	}
	go loadOnce.Do(func() { loadErr = dlopenAll() })
}

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
	webkitWebViewGetTitle                       func(view uintptr) string
	webkitWebViewGetSettings                    func(view uintptr) uintptr
	webkitWebViewGetContext                     func(view uintptr) uintptr
	webkitWebViewSetBackgroundColor             func(view uintptr, rgba uintptr)
	webkitSettingsSetHardwareAccelerationPolicy func(settings uintptr, policy int32)
	webkitSettingsSetEnableSmoothScrolling      func(settings uintptr, enabled int32)
	webkitSettingsSetEnableDeveloperExtras      func(settings uintptr, enabled int32)
	webkitWebContextSetCacheModel               func(ctx uintptr, model int32)
	gSignalConnectData                          func(instance uintptr, signal string, handler uintptr, data uintptr, destroyData uintptr, flags int32) uint64
	gTimeoutAddFull                             func(priority int32, interval uint32, function uintptr, data uintptr, notify uintptr) uint32

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
	libglib, err := purego.Dlopen("libglib-2.0.so.0", mode)
	if err != nil {
		return fmt.Errorf("dlopen libglib-2.0: %w", err)
	}
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
	purego.RegisterLibFunc(&webkitWebViewGetTitle, libwebkit, "webkit_web_view_get_title")
	purego.RegisterLibFunc(&webkitWebViewGetSettings, libwebkit, "webkit_web_view_get_settings")
	purego.RegisterLibFunc(&webkitWebViewGetContext, libwebkit, "webkit_web_view_get_context")
	purego.RegisterLibFunc(&webkitWebViewSetBackgroundColor, libwebkit, "webkit_web_view_set_background_color")
	purego.RegisterLibFunc(&webkitSettingsSetHardwareAccelerationPolicy, libwebkit, "webkit_settings_set_hardware_acceleration_policy")
	purego.RegisterLibFunc(&webkitSettingsSetEnableSmoothScrolling, libwebkit, "webkit_settings_set_enable_smooth_scrolling")
	purego.RegisterLibFunc(&webkitSettingsSetEnableDeveloperExtras, libwebkit, "webkit_settings_set_enable_developer_extras")
	purego.RegisterLibFunc(&webkitWebContextSetCacheModel, libwebkit, "webkit_web_context_set_cache_model")
	purego.RegisterLibFunc(&gSignalConnectData, libgobject, "g_signal_connect_data")
	purego.RegisterLibFunc(&gTimeoutAddFull, libglib, "g_timeout_add_full")
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

// gdkRGBA mirrors the GTK GdkRGBA struct (4 gdouble fields) so we can
// hand WebKit a background color pointer via purego.
type gdkRGBA struct {
	Red, Green, Blue, Alpha float64
}

// filterWebKitStderr redirects fd 2 through a pipe and drops a tiny
// allow-list of well-known JSC noise lines. Returns a restore function
// that closes the pipe and points fd 2 back at the original stderr.
// Anything not matched is forwarded verbatim, so panics and real
// errors still reach the user.
func filterWebKitStderr() (restore func()) {
	origFD, err := syscall.Dup(2)
	if err != nil {
		return func() {}
	}
	r, w, err := os.Pipe()
	if err != nil {
		_ = syscall.Close(origFD)
		return func() {}
	}
	if err := syscall.Dup2(int(w.Fd()), 2); err != nil {
		_ = r.Close()
		_ = w.Close()
		_ = syscall.Close(origFD)
		return func() {}
	}
	_ = w.Close()
	origStderr := os.NewFile(uintptr(origFD), "stderr-orig")
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 4096), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if dropJSCNoise(line) {
				continue
			}
			fmt.Fprintln(origStderr, line)
		}
	}()
	return func() {
		_ = syscall.Dup2(origFD, 2)
		_ = syscall.Close(origFD)
		_ = r.Close()
		<-done
	}
}

func dropJSCNoise(s string) bool {
	switch {
	case strings.HasPrefix(s, "ERROR: invalid option: JSC_SIGNAL_FOR_GC="):
		return true
	case strings.HasPrefix(s, "Overriding existing handler for signal "):
		return true
	}
	return false
}

func Open(opts Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	debug := os.Getenv("MDP_DEBUG") == "1"
	t0 := time.Now()
	mark := func(label string) {
		if debug {
			fmt.Fprintf(os.Stderr, "[mdp-time] %-22s %v\n", label, time.Since(t0))
		}
	}

	restore := filterWebKitStderr()
	defer restore()
	mark("after stderr filter")

	// Speeds up JSC JIT tier-up; noticeable on the wasm cold path.
	_ = os.Setenv("JSC_jitPolicyScale", "0.1")
	// JSC installs SIGUSR1 (signal 10) for GC by default and collides
	// with Go's runtime handlers ("Overriding existing handler for
	// signal 10"). JSC's signal-selection code reads JSC_SIGNAL_FOR_GC
	// (upper-case, contrary to its sibling JSC_* options) and accepts
	// a signal number. SIGUSR2 (12) also clashes with Go; real-time
	// signals don't. SIGRTMIN is usually 34 on glibc Linux with 32-33
	// reserved, so 35 is a safe slot.
	_ = os.Unsetenv("JSC_signalForGC")
	_ = os.Setenv("JSC_SIGNAL_FOR_GC", "35")
	// Skip the bubblewrap/seccomp sandbox setup on Web Process spawn.
	// We render local file:// markdown previews; the sandbox is part
	// of the ~100-150ms cost between load_uri and LOAD_STARTED.
	// WEBKIT_FORCE_SANDBOX was the legacy switch; modern WebKitGTK
	// (2.46+) ignores it (and prints a warning) and wants the
	// explicitly-named WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS
	// instead. Set only if the user hasn't pinned it themselves.
	if os.Getenv("WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS") == "" {
		_ = os.Setenv("WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS", "1")
	}

	loadOnce.Do(func() { loadErr = dlopenAll() })
	mark("loadOnce.Do (dlopen)")
	if loadErr != nil {
		return ErrUnsupported
	}

	var argc int32 = 0
	if gtkInitCheck(&argc, nil) == 0 {
		return fmt.Errorf("nativewin: gtk_init_check failed (no display?)")
	}
	mark("gtk_init_check")

	window := gtkWindowNew(gtkWindowToplevel)
	gtkWindowSetTitle(window, opts.titleOrDefault())
	gtkWindowSetDefSize(window, int32(opts.widthOrDefault()), int32(opts.heightOrDefault()))
	mark("gtk_window_new")

	view := webkitWebViewNew()
	mark("webkit_web_view_new")
	gtkContainerAdd(window, view)

	settings := webkitWebViewGetSettings(view)
	// ALWAYS gets the earliest first-contentful-paint in WebKit2GTK
	// despite the name: the software renderer (NEVER) ties FCP to the
	// window-load event because it paints only after subresources
	// resolve. Compositor-backed ALWAYS paints text as soon as layout
	// is ready, regardless of pending image fetches.
	webkitSettingsSetHardwareAccelerationPolicy(settings, webkitHardwareAccelerationAlways)
	webkitSettingsSetEnableSmoothScrolling(settings, 0)
	if debug {
		// Right-click → Inspect Element opens DevTools so the user can
		// read the JS-side timeline (performance panel, console.log).
		webkitSettingsSetEnableDeveloperExtras(settings, 1)
	}

	webkitWebContextSetCacheModel(webkitWebViewGetContext(view), webkitCacheModelWebBrowser)
	mark("webkit settings done")

	// Paint the WebKit viewport with the GitHub-dark background before
	// the first frame so the window doesn't flash white during the
	// load. Matches --color-bg-primary in the page CSS.
	bg := gdkRGBA{Red: 13.0 / 255, Green: 17.0 / 255, Blue: 23.0 / 255, Alpha: 1.0}
	webkitWebViewSetBackgroundColor(view, uintptr(unsafe.Pointer(&bg)))

	// Defer the window show until WebKit fires LOAD_FINISHED so the
	// window appears with content already painted rather than as a
	// dark rectangle waiting on first-contentful-paint. FCP in
	// WebKit2GTK lands ~600ms after LOAD_COMMITTED on cold start; the
	// LOAD_FINISHED signal is the closest WebKit-exposed proxy. As a
	// safety net, a 1500ms timer also shows the window in case a
	// broken URL means LOAD_FINISHED never fires. WEBKIT_LOAD_FINISHED
	// event value is 3.
	shown := false
	showOnce := func(reason string) {
		if shown {
			return
		}
		shown = true
		gtkWidgetShowAll(window)
		mark(fmt.Sprintf("gtk_widget_show_all (%s)", reason))
	}
	loadCB := purego.NewCallback(func(_view, event, _data uintptr) uintptr {
		if debug {
			mark(fmt.Sprintf("load-changed event=%d", event))
		}
		if event == webkitLoadFinished {
			showOnce("LOAD_FINISHED")
		}
		return 0
	})
	gSignalConnectData(view, "load-changed", loadCB, 0, 0, 0)
	// Mirror the WebView's document title to the GTK window title so
	// the window header reflects "parent/basename" instead of the
	// "mdp" placeholder set at window creation.
	titleCB := purego.NewCallback(func(_view, _pspec, _data uintptr) uintptr {
		if t := webkitWebViewGetTitle(view); t != "" {
			gtkWindowSetTitle(window, t)
		}
		return 0
	})
	gSignalConnectData(view, "notify::title", titleCB, 0, 0, 0)
	// Safety net: if the load stalls or the signal never fires, show
	// the window anyway after 1.5s rather than leaving the user with
	// nothing.
	timeoutCB := purego.NewCallback(func(_data uintptr) uintptr {
		showOnce("timeout")
		return 0 // G_SOURCE_REMOVE
	})
	gTimeoutAddFull(0, 1500, timeoutCB, 0, 0)

	webkitWebViewLoadURI(view, opts.URL)
	mark("webkit_web_view_load_uri")

	destroyCB := purego.NewCallback(func(_ uintptr, _ uintptr) uintptr {
		gtkMainQuit()
		return 0
	})
	gSignalConnectData(window, "destroy", destroyCB, 0, 0, 0)

	gtkMain()
	return nil
}

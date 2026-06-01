//go:build darwin

package nativewin

import (
	"fmt"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Cocoa + WKWebView driven via purego/objc so the build stays CGO-free.
// darwinkit was the first plan but its helper/action and dispatch
// packages pull in #cgo directives, so this file talks to objc_msgSend
// directly.

const (
	nsApplicationActivationPolicyAccessory = 1
	nsBackingStoreBuffered                 = 2
	nsWindowStyleMaskTitled                = 1 << 0
	nsWindowStyleMaskClosable              = 1 << 1
	nsWindowStyleMaskMiniaturizable        = 1 << 2
	nsWindowStyleMaskResizable             = 1 << 3
	nsEventTypeApplicationDefined          = 15
)

type nsPoint struct{ X, Y float64 }
type nsSize struct{ W, H float64 }
type nsRect struct {
	Origin nsPoint
	Size   nsSize
}

var (
	frameworksOnce sync.Once
	frameworksErr  error
)

func loadFrameworks() {
	for _, path := range []string{
		"/System/Library/Frameworks/Cocoa.framework/Cocoa",
		"/System/Library/Frameworks/WebKit.framework/WebKit",
	} {
		if _, err := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_LAZY); err != nil {
			frameworksErr = fmt.Errorf("dlopen %s: %w", path, err)
			return
		}
	}
}

// Available returns false only when Cocoa/WebKit can't be dlopened,
// which is effectively impossible on a real Mac.
func Available() bool {
	frameworksOnce.Do(loadFrameworks)
	return frameworksErr == nil
}

// Open blocks until the user closes the window. Must run on a
// goroutine locked to the OS main thread; AppKit's run loop requires it.
func Open(opts Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	frameworksOnce.Do(loadFrameworks)
	if frameworksErr != nil {
		return ErrUnsupported
	}

	// Autoreleased class-method returns (stringWithUTF8String:,
	// URLWithString:, requestWithURL:) leak for the process lifetime
	// without a pool to drain after NSApp.run returns.
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("init"))
	defer pool.Send(objc.RegisterName("drain"))

	nsApp := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
	// Accessory: window can take focus but mdp doesn't appear in the Dock
	// or the menu bar. Right shape for a CLI markdown previewer.
	nsApp.Send(objc.RegisterName("setActivationPolicy:"), nsApplicationActivationPolicyAccessory)

	mask := nsWindowStyleMaskTitled | nsWindowStyleMaskClosable | nsWindowStyleMaskMiniaturizable | nsWindowStyleMaskResizable
	frame := nsRect{Size: nsSize{W: float64(opts.widthOrDefault()), H: float64(opts.heightOrDefault())}}
	wnd := objc.ID(objc.GetClass("NSWindow")).Send(objc.RegisterName("alloc"))
	wnd = wnd.Send(
		objc.RegisterName("initWithContentRect:styleMask:backing:defer:"),
		frame,
		uintptr(mask),
		nsBackingStoreBuffered,
		false,
	)

	title := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), opts.titleOrDefault())
	wnd.Send(objc.RegisterName("setTitle:"), title)

	configClass := objc.ID(objc.GetClass("WKWebViewConfiguration"))
	config := configClass.Send(objc.RegisterName("alloc")).Send(objc.RegisterName("init"))
	view := objc.ID(objc.GetClass("WKWebView")).Send(objc.RegisterName("alloc"))
	view = view.Send(objc.RegisterName("initWithFrame:configuration:"), frame, config)
	wnd.Send(objc.RegisterName("setContentView:"), view)

	urlStr := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), opts.URL)
	nsURL := objc.ID(objc.GetClass("NSURL")).Send(objc.RegisterName("URLWithString:"), urlStr)
	req := objc.ID(objc.GetClass("NSURLRequest")).Send(objc.RegisterName("requestWithURL:"), nsURL)
	view.Send(objc.RegisterName("loadRequest:"), req)

	// NSWindow's delegate ref is weak/zeroing, so the Go-allocated
	// object needs an explicit retain to outlive Open's stack frame.
	delegateClass, err := registerWindowDelegate()
	if err != nil {
		return fmt.Errorf("nativewin: register delegate: %w", err)
	}
	delegate := objc.ID(delegateClass).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("init"))
	delegate.Send(objc.RegisterName("retain"))
	wnd.Send(objc.RegisterName("setDelegate:"), delegate)
	// KVO on WKWebView.title — the delegate's observeValueForKeyPath:
	// hook mirrors it to the NSWindow. NSKeyValueObservingOptionNew = 1.
	titleKey := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), "title")
	view.Send(
		objc.RegisterName("addObserver:forKeyPath:options:context:"),
		delegate,
		titleKey,
		uintptr(1),
		uintptr(0),
	)

	// center BEFORE makeKeyAndOrderFront: otherwise the window flashes
	// at the origin-derived position before settling (Apple docs).
	wnd.Send(objc.RegisterName("center"))
	wnd.Send(objc.RegisterName("makeKeyAndOrderFront:"), objc.ID(0))
	nsApp.Send(objc.RegisterName("activateIgnoringOtherApps:"), true)
	nsApp.Send(objc.RegisterName("run"))
	return nil
}

var (
	delegateClassOnce sync.Once
	delegateClass     objc.Class
	delegateClassErr  error
)

func registerWindowDelegate() (objc.Class, error) {
	delegateClassOnce.Do(func() {
		delegateClass, delegateClassErr = objc.RegisterClass(
			"MdpWindowDelegate",
			objc.GetClass("NSObject"),
			nil,
			nil,
			[]objc.MethodDef{
				{
					Cmd: objc.RegisterName("windowWillClose:"),
					Fn:  onWindowWillClose,
				},
				{
					// KVO callback wired below to the WKWebView's
					// "title" key path so the NSWindow title follows
					// document.title — same effect as the GTK
					// notify::title hook on Linux.
					Cmd: objc.RegisterName("observeValueForKeyPath:ofObject:change:context:"),
					Fn:  onObserveTitle,
				},
			},
		)
	})
	return delegateClass, delegateClassErr
}

// onObserveTitle fires whenever the WKWebView's title KVO key
// changes. We only register for "title", so any callback here is a
// title update; copy it onto the owning NSWindow so the chrome
// reflects document.title.
func onObserveTitle(_ objc.ID, _ objc.SEL, _ objc.ID, object objc.ID, _ objc.ID, _ uintptr) {
	if object == 0 {
		return
	}
	title := object.Send(objc.RegisterName("title"))
	if title == 0 {
		return
	}
	wnd := object.Send(objc.RegisterName("window"))
	if wnd == 0 {
		return
	}
	wnd.Send(objc.RegisterName("setTitle:"), title)
}

// [NSApp stop:] only takes effect at the end of the current
// event-handling iteration, so programmatic closes (no event in flight)
// would stall the run loop. Posting a dummy event forces the next tick.
func onWindowWillClose(_ objc.ID, _ objc.SEL, _ objc.ID) {
	nsApp := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
	nsApp.Send(objc.RegisterName("stop:"), objc.ID(0))

	ev := objc.ID(objc.GetClass("NSEvent")).Send(
		objc.RegisterName("otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:"),
		uintptr(nsEventTypeApplicationDefined),
		nsPoint{},
		uintptr(0),
		float64(0),
		uintptr(0),
		objc.ID(0),
		uintptr(0),
		uintptr(0),
		uintptr(0),
	)
	nsApp.Send(objc.RegisterName("postEvent:atStart:"), ev, true)
}

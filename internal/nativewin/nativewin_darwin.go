//go:build darwin

package nativewin

import (
	"fmt"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Cocoa + WKWebView via pure purego/objc, no darwinkit, no CGO. We
// dlopen Cocoa.framework (Foundation + AppKit symbols) and
// WebKit.framework (WKWebView), then drive everything via objc_msgSend.
//
// The code is dense because every Cocoa call goes through
// objc.RegisterName + ID.Send. The structure mirrors what you'd write in
// 30 lines of Objective-C:
//
//   [NSApplication sharedApplication]
//   [NSApp setActivationPolicy:Accessory]
//   [[NSWindow alloc] initWithContentRect:... styleMask:... ...]
//   [window setTitle:@"mdp"]
//   [window setContentView: [[WKWebView alloc] initWithFrame:... configuration:...]]
//   [view loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:...]]]
//   [window center]
//   [window makeKeyAndOrderFront:nil]
//   [NSApp activateIgnoringOtherApps:YES]
//   [NSApp run]   // blocks
//
// Window-close handling: we register an NSObject subclass with a
// windowWillClose: method, set it as the window delegate, and call
// [NSApp stop:nil] plus post a dummy event from inside. NSApp.run()
// returns on the next event tick, so Open() returns cleanly and the
// caller shuts the server down.

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

// Available reports whether the native-window backend is usable. WKWebView
// ships with every macOS, so the only way this returns false is if
// Cocoa/WebKit somehow can't be dlopened (essentially impossible).
func Available() bool {
	frameworksOnce.Do(loadFrameworks)
	return frameworksErr == nil
}

// Open creates an NSWindow with a WKWebView pointed at opts.URL and
// blocks until the user closes the window.
//
// Must be called from a goroutine locked to the OS main thread (Cocoa
// requires the AppKit run loop on the main thread).
func Open(opts Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	frameworksOnce.Do(loadFrameworks)
	if frameworksErr != nil {
		return ErrUnsupported
	}

	// Autorelease pool wraps every Cocoa call here so the autoreleased
	// objects returned by class methods (stringWithUTF8String:,
	// URLWithString:, requestWithURL:) get cleaned up after NSApp.run
	// returns. Without this, [NSString stringWithUTF8String:] etc. leak
	// for the process lifetime.
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("init"))
	defer pool.Send(objc.RegisterName("drain"))

	// [NSApplication sharedApplication]
	nsApp := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
	// Accessory: window can take focus but mdp doesn't appear in the Dock
	// or the menu bar. Right shape for a CLI markdown previewer.
	nsApp.Send(objc.RegisterName("setActivationPolicy:"), nsApplicationActivationPolicyAccessory)

	// [[NSWindow alloc] initWithContentRect:rect styleMask:mask backing:Buffered defer:NO]
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

	// [window setTitle: @"..."]
	title := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), opts.titleOrDefault())
	wnd.Send(objc.RegisterName("setTitle:"), title)

	// WKWebView with default configuration
	configClass := objc.ID(objc.GetClass("WKWebViewConfiguration"))
	config := configClass.Send(objc.RegisterName("alloc")).Send(objc.RegisterName("init"))
	view := objc.ID(objc.GetClass("WKWebView")).Send(objc.RegisterName("alloc"))
	view = view.Send(objc.RegisterName("initWithFrame:configuration:"), frame, config)
	wnd.Send(objc.RegisterName("setContentView:"), view)

	// [view loadRequest: [NSURLRequest requestWithURL: [NSURL URLWithString: @"..."]]]
	urlStr := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), opts.URL)
	nsURL := objc.ID(objc.GetClass("NSURL")).Send(objc.RegisterName("URLWithString:"), urlStr)
	req := objc.ID(objc.GetClass("NSURLRequest")).Send(objc.RegisterName("requestWithURL:"), nsURL)
	view.Send(objc.RegisterName("loadRequest:"), req)

	// Window delegate stops the run loop on close. NSWindow holds a
	// weak/zeroing reference to its delegate, so we retain explicitly to
	// keep the Go-allocated object alive past Open's stack frame.
	delegateClass, err := registerWindowDelegate()
	if err != nil {
		return fmt.Errorf("nativewin: register delegate: %w", err)
	}
	delegate := objc.ID(delegateClass).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("init"))
	delegate.Send(objc.RegisterName("retain"))
	wnd.Send(objc.RegisterName("setDelegate:"), delegate)

	// center BEFORE makeKeyAndOrderFront: per Apple docs, otherwise the
	// window can flash at the origin-derived position before settling.
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
			},
		)
	})
	return delegateClass, delegateClassErr
}

// onWindowWillClose stops the NSApp run loop and posts a dummy event so
// the loop unblocks immediately even when the close came from
// non-event-driven paths (programmatic [window close], Cmd-Q via
// menu-less app, IPC quit). Without the posted event, [NSApp stop:] is
// only checked at the end of the current event-handling iteration,
// which a window-close-from-code can skip.
func onWindowWillClose(_ objc.ID, _ objc.SEL, _ objc.ID) {
	nsApp := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
	nsApp.Send(objc.RegisterName("stop:"), objc.ID(0))

	// [NSEvent otherEventWithType:NSEventTypeApplicationDefined
	//                    location:NSZeroPoint modifierFlags:0 timestamp:0
	//                windowNumber:0 context:nil subtype:0 data1:0 data2:0]
	ev := objc.ID(objc.GetClass("NSEvent")).Send(
		objc.RegisterName("otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:"),
		uintptr(nsEventTypeApplicationDefined),
		nsPoint{},
		uintptr(0), // modifier flags
		float64(0), // timestamp (NSTimeInterval)
		uintptr(0), // window number
		objc.ID(0), // context
		uintptr(0), // subtype (short)
		uintptr(0), // data1
		uintptr(0), // data2
	)
	nsApp.Send(objc.RegisterName("postEvent:atStart:"), ev, true)
}

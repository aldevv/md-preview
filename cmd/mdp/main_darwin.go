//go:build darwin

package main

import "runtime"

// Cocoa's NSApp run loop refuses to start on anything but the OS main
// thread. Locking the main goroutine here keeps it pinned for the
// process lifetime so mdp can open a native window (the default on
// macOS; opt out with MDP_NATIVE=0).
func init() {
	runtime.LockOSThread()
}

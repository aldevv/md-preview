//go:build darwin

package main

import "runtime"

// Cocoa's NSApp run loop refuses to start on anything but the OS main
// thread. Locking the main goroutine here keeps it pinned for the
// process lifetime so `mdp watch` with MDP_NATIVE=1 can open a window.
func init() {
	runtime.LockOSThread()
}

//go:build !windows

// Package osutil contains the tiny platform-specific shims mdp needs
// (process detach attrs, syscall.Exec replacement, O_NOFOLLOW for
// secure tmp file writes). Unix variant.
package osutil

import "syscall"

// ONoFollow is the open(2) flag that refuses to traverse symlinks at
// the final path component (defense against shared-tmp symlink swap).
// Windows has no equivalent so the constant is 0 there.
const ONoFollow = syscall.O_NOFOLLOW

// DetachAttr returns SysProcAttr fields that make a child process
// outlive its parent's terminal. Unix uses Setsid; Windows uses the
// DETACHED_PROCESS + CREATE_NEW_PROCESS_GROUP flags.
func DetachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// ReplaceProcess execs path in place of the current process on unix
// (never returns on success). Windows can't exec; the windows variant
// runs the command as a child, mirrors its exit code, and os.Exits.
func ReplaceProcess(path string, argv []string, env []string) error {
	return syscall.Exec(path, argv, env)
}

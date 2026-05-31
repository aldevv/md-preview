//go:build windows

package osutil

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ONoFollow is 0 on Windows: the platform has no analogous flag and
// the shared-tmp symlink threat the unix sites guard against is
// markedly less of a concern under Windows' file semantics. Callers
// pass it OR'd into os.OpenFile flags; 0 is a safe no-op.
const ONoFollow = 0

// Windows process creation flags. syscall doesn't export these as
// constants on every Go version, so we inline the values here. See
// https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags
const (
	detachedProcess        = 0x00000008
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// DetachAttr returns SysProcAttr flags that make a child outlive its
// parent's console: DETACHED_PROCESS removes the inherited console,
// CREATE_NEW_PROCESS_GROUP makes the child immune to Ctrl-C/Ctrl-Break
// directed at the parent.
func DetachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: detachedProcess | createNewProcessGroup,
	}
}

// ReplaceProcess can't actually replace the current process on
// Windows (no execve equivalent), so we run the target as a child,
// proxy its exit code, and os.Exit. Caller never returns from this
// function on success, matching the unix syscall.Exec contract the
// rest of mdp expects.
func ReplaceProcess(path string, argv []string, env []string) error {
	cmd := exec.Command(path)
	cmd.Args = argv
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	os.Exit(0)
	return nil // unreachable
}

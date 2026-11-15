//go:build unix

package app

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// spawnSharedServer re-executes this launch as a detached shared server.
func spawnSharedServer(identity string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve the executable: %w", err)
	}
	return startDetached(sharedServerCommand(executable, os.Args[1:], identity))
}

// sharedServerCommand prepends the identity flag so that an argument
// terminator in the launch cannot turn it into a positional argument.
func sharedServerCommand(executable string, args []string, identity string) *exec.Cmd {
	return detachedCommand(executable, append([]string{"--" + sharedServerIdentityFlag, identity}, args...)...)
}

// detachedCommand runs in a session of its own, so the host's terminal and
// process group signals do not reach it.
func detachedCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// startDetached releases the started process instead of waiting on it: its
// outcome is observed only through the endpoint.
func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

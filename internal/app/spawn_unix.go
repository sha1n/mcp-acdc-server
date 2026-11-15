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

// startDetached does not wait for the started process, because its outcome is
// observed only through the endpoint. The background wait exists solely to reap
// candidates that exit, such as those losing the identity lock, so they do not
// linger as zombies for the lifetime of the launching process.
func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

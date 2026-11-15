//go:build unix

package app

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// spawnSharedServer re-executes this launch as a detached shared server. The
// candidate is released, never waited on: its outcome is observed only
// through the endpoint.
func spawnSharedServer(identity string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve the executable: %w", err)
	}
	cmd := sharedServerCommand(executable, os.Args[1:], identity)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// sharedServerCommand prepends the identity flag so that an argument
// terminator in the launch cannot turn it into a positional argument.
func sharedServerCommand(executable string, args []string, identity string) *exec.Cmd {
	cmd := exec.Command(executable, append([]string{"--" + sharedServerIdentityFlag, identity}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

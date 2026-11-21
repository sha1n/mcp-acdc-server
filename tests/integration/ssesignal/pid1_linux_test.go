//go:build linux

package ssesignal

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// asPID1 runs the command as PID 1 of a new PID namespace, owned by a new
// user namespace so no privilege is needed. The kernel drops a signal sent
// to a namespace init from outside unless init handles it, so the default
// SIGTERM disposition cannot end the process there.
func asPID1(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER | syscall.CLONE_NEWPID,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
		GidMappingsEnableSetgroups: false,
	}
}

// skipWithoutPIDNamespaces skips only when the host does not support or
// denies unprivileged user and PID namespaces; any other probe failure, such
// as a missing probe executable, fails the test so PID 1 coverage is not
// silently lost.
func skipWithoutPIDNamespaces(t *testing.T) {
	t.Helper()
	probe := exec.Command("true")
	asPID1(probe)
	var stderr bytes.Buffer
	probe.Stderr = &stderr
	err := probe.Run()
	if err == nil {
		return
	}
	if namespacesUnavailable(err, stderr.String()) {
		t.Skipf("unprivileged user and PID namespaces are unavailable: %v", err)
	}
	t.Fatalf("the PID namespace probe failed: %v\nstderr:\n%s", err, &stderr)
}

func namespacesUnavailable(err error, stderr string) bool {
	for _, denied := range []error{syscall.EPERM, syscall.EACCES, syscall.EINVAL} {
		if errors.Is(err, denied) {
			return true
		}
	}
	return strings.Contains(stderr, "Operation not permitted")
}

func TestSSESignal_AsPID1FirstSIGTERMExitsZero(t *testing.T) {
	skipWithoutPIDNamespaces(t)
	p := startSSE(t, launchOptions{configure: asPID1})

	start := time.Now()
	p.signal(t, syscall.SIGTERM)
	p.awaitPromptExit(t, start)
	p.requireExitCodeZero(t)
}

func TestSSESignal_AsPID1SecondSIGTERMExits143(t *testing.T) {
	skipWithoutPIDNamespaces(t)
	p := startSSE(t, launchOptions{configure: asPID1})
	requireSecondSignalExits(t, p, syscall.SIGTERM)
}

//go:build unix

package testkit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingReporter struct{ failures []string }

func (r *recordingReporter) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// startFakeSharedServer starts a long-running process whose arguments look
// like a shared server's, so the guard treats it as one.
func startFakeSharedServer(t *testing.T, linger string) (pid int, exited <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "sleep 600; :", "sh", "--shared-server-identity", "0123456789abcdef0123456789abcdef", "--shared-server-linger", linger)
	require.NoError(t, cmd.Start())
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	return cmd.Process.Pid, done
}

func writeFakeServerLog(t *testing.T, pid int, started time.Time) string {
	t.Helper()
	dir := t.TempDir()
	line := fmt.Sprintf("time=%s level=INFO msg=%q version=dev pid=%d\n", started.Format(time.RFC3339Nano), sharedServerStartupMsg, pid)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0123456789abcdef0123456789abcdef.log"), []byte(line), 0o600))
	return dir
}

func awaitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("the fake shared server was not killed")
	}
}

func TestCheckSharedServerLeaks_KillsAndReportsAServerPastItsDeadline(t *testing.T) {
	pid, exited := startFakeSharedServer(t, "1s")
	logDir := writeFakeServerLog(t, pid, time.Now().Add(-time.Minute))
	reporter := &recordingReporter{}

	CheckSharedServerLeaks(reporter, logDir, time.Second)

	awaitClosed(t, exited)
	require.Len(t, reporter.failures, 1)
	assert.Contains(t, reporter.failures[0], "alive after its linger deadline")
}

func TestCheckSharedServerLeaks_KillsWithoutReportingAServerInsideItsDeadline(t *testing.T) {
	pid, exited := startFakeSharedServer(t, "10m")
	logDir := writeFakeServerLog(t, pid, time.Now())
	reporter := &recordingReporter{}

	CheckSharedServerLeaks(reporter, logDir, time.Second)

	awaitClosed(t, exited)
	assert.Empty(t, reporter.failures)
}

func TestCheckSharedServerLeaks_LeavesAProcessThatIsNotASharedServer(t *testing.T) {
	cmd := exec.Command("sleep", "600")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	logDir := writeFakeServerLog(t, cmd.Process.Pid, time.Now().Add(-time.Minute))
	reporter := &recordingReporter{}

	CheckSharedServerLeaks(reporter, logDir, time.Second)

	assert.Empty(t, reporter.failures)
	assert.NoError(t, syscall.Kill(cmd.Process.Pid, 0), "a reused pid must not be killed")
}

func TestCheckSharedServerLeaks_ReportsALingerExpiryAfterTheDeadline(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().Add(-time.Hour)
	log := fmt.Sprintf("time=%s level=INFO msg=%q version=dev pid=1\n", started.Format(time.RFC3339Nano), sharedServerStartupMsg) +
		fmt.Sprintf("time=%s level=INFO msg=\"Shared server linger expired with no session attached\" linger=1s\n", started.Add(time.Minute).Format(time.RFC3339Nano)) +
		fmt.Sprintf("time=%s level=INFO msg=\"Shared server stopped\"\n", started.Add(time.Minute).Format(time.RFC3339Nano))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0123456789abcdef0123456789abcdef.log"), []byte(log), 0o600))
	reporter := &recordingReporter{}

	CheckSharedServerLeaks(reporter, dir, time.Second)

	require.Len(t, reporter.failures, 1)
	assert.Contains(t, reporter.failures[0], "after its linger deadline")
}

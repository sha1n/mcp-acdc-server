//go:build unix

package testkit

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fakeIdentity        = "0123456789abcdef0123456789abcdef"
	otherIdentity       = "ffffffffffffffffffffffffffffffff"
	fakeSharedServerEnv = "ACDC_TESTKIT_FAKE_SHARED_SERVER"
	guardTestBudget     = 30 * time.Second
)

type recordingReporter struct{ failures []string }

func (r *recordingReporter) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// TestFakeSharedServerProcess is the body of a fake shared server, not a
// test: it echoes stdin lines, which proves it alive, and ends at stdin EOF,
// so it ends by itself once its test lets go of it, even a crashed one.
func TestFakeSharedServerProcess(t *testing.T) {
	if os.Getenv(fakeSharedServerEnv) != "1" {
		t.Skip("runs only as the fake shared server of the leak guard tests")
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	os.Exit(0)
}

type fakeSharedServer struct {
	pid    int
	stdin  *os.File
	stdout *os.File
	lines  *bufio.Scanner
	exited chan struct{}
}

// startFakeSharedServer starts one process, with no descendants, in its own
// process group, running in launchDir with args among its arguments.
func startFakeSharedServer(t *testing.T, deadline time.Time, launchDir string, args ...string) *fakeSharedServer {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestFakeSharedServerProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), fakeSharedServerEnv+"=1")
	cmd.Dir = launchDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	childIn, stdin, err := os.Pipe()
	require.NoError(t, err)
	stdout, childOut, err := os.Pipe()
	require.NoError(t, err)
	cmd.Stdin, cmd.Stdout = childIn, childOut
	require.NoError(t, cmd.Start())
	_ = childIn.Close()
	_ = childOut.Close()
	f := &fakeSharedServer{pid: cmd.Process.Pid, stdin: stdin, stdout: stdout, lines: bufio.NewScanner(stdout), exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(f.exited)
	}()
	t.Cleanup(func() {
		_ = f.stdin.Close()
		select {
		case <-f.exited:
		case <-time.After(time.Until(deadline)):
			_ = cmd.Process.Kill()
			<-f.exited
			t.Errorf("the fake shared server did not end at stdin EOF")
		}
		_ = f.stdout.Close()
		assert.ErrorIs(t, syscall.Kill(-f.pid, 0), syscall.ESRCH, "the fake shared server left a process in its group")
	})
	return f
}

// requireAlive proves the process alive by a round trip through it.
func (f *fakeSharedServer) requireAlive(t *testing.T, deadline time.Time) {
	t.Helper()
	require.NoError(t, f.stdout.SetReadDeadline(deadline))
	_, err := fmt.Fprintln(f.stdin, "ping")
	require.NoError(t, err, "the fake shared server is gone")
	require.True(t, f.lines.Scan(), "the fake shared server is gone: %v", f.lines.Err())
	require.Equal(t, "ping", f.lines.Text())
}

func (f *fakeSharedServer) requireExited(t *testing.T, deadline time.Time) {
	t.Helper()
	select {
	case <-f.exited:
	case <-time.After(time.Until(deadline)):
		t.Fatal("the fake shared server was not killed")
	}
}

func sharedServerArgs(identity, linger string) []string {
	return []string{"--shared-server-identity", identity, "--shared-server-linger", linger}
}

func writeFakeServerLog(t *testing.T, pid int, started time.Time) string {
	t.Helper()
	dir := t.TempDir()
	line := fmt.Sprintf("time=%s level=INFO msg=%q version=dev pid=%d\n", started.Format(time.RFC3339Nano), sharedServerStartupMsg, pid)
	require.NoError(t, os.WriteFile(filepath.Join(dir, fakeIdentity+".log"), []byte(line), 0o600))
	return dir
}

func TestCheckSharedServerLeaks_KillsAndReportsAServerPastItsDeadline(t *testing.T) {
	deadline := time.Now().Add(guardTestBudget)
	launchDir := t.TempDir()
	server := startFakeSharedServer(t, deadline, launchDir, sharedServerArgs(fakeIdentity, "1s")...)
	logDir := writeFakeServerLog(t, server.pid, time.Now().Add(-time.Minute))
	reporter := &recordingReporter{}

	CheckSharedServerLeaks(reporter, logDir, launchDir, time.Second)

	server.requireExited(t, deadline)
	require.Len(t, reporter.failures, 1)
	assert.Contains(t, reporter.failures[0], "alive after its linger deadline")
}

func TestCheckSharedServerLeaks_KillsWithoutReportingAServerInsideItsDeadline(t *testing.T) {
	deadline := time.Now().Add(guardTestBudget)
	launchDir := t.TempDir()
	server := startFakeSharedServer(t, deadline, launchDir, sharedServerArgs(fakeIdentity, "10m")...)
	logDir := writeFakeServerLog(t, server.pid, time.Now())
	reporter := &recordingReporter{}

	CheckSharedServerLeaks(reporter, logDir, launchDir, time.Second)

	server.requireExited(t, deadline)
	assert.Empty(t, reporter.failures)
}

// A pid that no longer names this log's server is a server that is gone:
// the process holding it now is neither killed nor reported.
func TestCheckSharedServerLeaks_LeavesAProcessThatIsNotThisLogsServer(t *testing.T) {
	launchDir := t.TempDir()
	for name, process := range map[string]struct {
		launchDir string
		args      []string
	}{
		"not a shared server":                 {launchDir, nil},
		"a shared server of another identity": {launchDir, sharedServerArgs(otherIdentity, "1s")},
		"a shared server of another launch":   {t.TempDir(), sharedServerArgs(fakeIdentity, "1s")},
		"another identity given inline":       {launchDir, []string{"--shared-server-identity=" + otherIdentity}},
		"the identity only as another flag's": {launchDir, []string{"--x", fakeIdentity, "--shared-server-identity", otherIdentity}},
	} {
		t.Run(name, func(t *testing.T) {
			deadline := time.Now().Add(guardTestBudget)
			server := startFakeSharedServer(t, deadline, process.launchDir, process.args...)
			logDir := writeFakeServerLog(t, server.pid, time.Now().Add(-time.Minute))
			reporter := &recordingReporter{}

			CheckSharedServerLeaks(reporter, logDir, launchDir, time.Second)

			server.requireAlive(t, deadline)
			assert.Empty(t, reporter.failures)
		})
	}
}

func TestCheckSharedServerLeaks_KillsAServerWhoseIdentityIsGivenInline(t *testing.T) {
	deadline := time.Now().Add(guardTestBudget)
	launchDir := t.TempDir()
	server := startFakeSharedServer(t, deadline, launchDir, "--shared-server-identity="+fakeIdentity)
	logDir := writeFakeServerLog(t, server.pid, time.Now())
	reporter := &recordingReporter{}

	CheckSharedServerLeaks(reporter, logDir, launchDir, time.Second)

	server.requireExited(t, deadline)
}

func TestCheckSharedServerLeaks_ReportsALingerExpiryAfterTheDeadline(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().Add(-time.Hour)
	log := fmt.Sprintf("time=%s level=INFO msg=%q version=dev pid=1\n", started.Format(time.RFC3339Nano), sharedServerStartupMsg) +
		fmt.Sprintf("time=%s level=INFO msg=\"Shared server linger expired with no session attached\" linger=1s\n", started.Add(time.Minute).Format(time.RFC3339Nano)) +
		fmt.Sprintf("time=%s level=INFO msg=\"Shared server stopped\"\n", started.Add(time.Minute).Format(time.RFC3339Nano))
	require.NoError(t, os.WriteFile(filepath.Join(dir, fakeIdentity+".log"), []byte(log), 0o600))
	reporter := &recordingReporter{}

	CheckSharedServerLeaks(reporter, dir, t.TempDir(), time.Second)

	require.Len(t, reporter.failures, 1)
	assert.Contains(t, reporter.failures[0], "after its linger deadline")
}

// inspectionPath is a PATH holding only the real tools named in real and a
// script per entry of scripts.
func inspectionPath(t *testing.T, real []string, scripts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range real {
		path, err := exec.LookPath(name)
		require.NoError(t, err)
		require.NoError(t, os.Symlink(path, filepath.Join(dir, name)))
	}
	for name, script := range scripts {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o700))
	}
	return dir
}

// An overdue server in its own launch directory makes a silent pass a missed
// leak, so a failed inspection has to be reported, and without proof of
// ownership the process must not be signalled.
func requireReportedUnsignalled(t *testing.T, deadline time.Time, server *fakeSharedServer, reporter *recordingReporter, cause string) {
	t.Helper()
	server.requireAlive(t, deadline)
	require.NotEmpty(t, reporter.failures)
	for _, failure := range reporter.failures {
		assert.Contains(t, failure, fmt.Sprint(server.pid))
		assert.Contains(t, failure, fakeIdentity)
		assert.Contains(t, failure, cause)
	}
}

// Only the macOS branch reads the working directory through a tool; the
// Linux branch reads /proc/<pid>/cwd, which an unprivileged test cannot make
// unreadable for its own child, so there its failures are covered by
// TestClassifyLookFailure_TellsAGoneProcessFromAnUninspectableOne.
func TestCheckSharedServerLeaks_ReportsWithoutSignallingAServerWhoseCwdCannotBeInspected(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("the Linux working directory lookup runs no tool")
	}
	deadline := time.Now().Add(guardTestBudget)
	launchDir := t.TempDir()
	server := startFakeSharedServer(t, deadline, launchDir, sharedServerArgs(fakeIdentity, "1s")...)
	server.requireAlive(t, deadline)
	logDir := writeFakeServerLog(t, server.pid, time.Now().Add(-time.Minute))
	reporter := &recordingReporter{}
	t.Setenv("PATH", inspectionPath(t, []string{"ps"}, map[string]string{"lsof": "exit 1"}))

	CheckSharedServerLeaks(reporter, logDir, launchDir, time.Second)

	requireReportedUnsignalled(t, deadline, server, reporter, "lsof")
}

func TestCheckSharedServerLeaks_ReportsWithoutSignallingAServerWhoseArgumentsCannotBeInspected(t *testing.T) {
	deadline := time.Now().Add(guardTestBudget)
	launchDir := t.TempDir()
	server := startFakeSharedServer(t, deadline, launchDir, sharedServerArgs(fakeIdentity, "1s")...)
	server.requireAlive(t, deadline)
	logDir := writeFakeServerLog(t, server.pid, time.Now().Add(-time.Minute))
	reporter := &recordingReporter{}
	t.Setenv("PATH", inspectionPath(t, nil, map[string]string{"ps": "exit 1"}))

	CheckSharedServerLeaks(reporter, logDir, launchDir, time.Second)

	requireReportedUnsignalled(t, deadline, server, reporter, "ps")
}

func TestCheckSharedServerLeaks_ReportsAHungInspectionWithinItsWatchdog(t *testing.T) {
	deadline := time.Now().Add(guardTestBudget)
	launchDir := t.TempDir()
	server := startFakeSharedServer(t, deadline, launchDir, sharedServerArgs(fakeIdentity, "1s")...)
	server.requireAlive(t, deadline)
	logDir := writeFakeServerLog(t, server.pid, time.Now().Add(-time.Minute))
	reporter := &recordingReporter{}
	// exec leaves the hung tool without a descendant, and sleep ends by
	// itself should the guard fail to stop it.
	t.Setenv("PATH", inspectionPath(t, nil, map[string]string{"ps": "exec /bin/sleep 30"}))
	const watchdog = 500 * time.Millisecond
	done := make(chan struct{})

	go func() {
		defer close(done)
		checkSharedServerLeaks(reporter, logDir, launchDir, time.Second, watchdog)
	}()

	select {
	case <-done:
	case <-time.After(watchdog + inspectionWaitDelay + 2*time.Second):
		t.Fatal("the leak guard outlived its watchdog")
	}
	requireReportedUnsignalled(t, deadline, server, reporter, context.DeadlineExceeded.Error())
}

func TestClassifyLookFailure_TellsAGoneProcessFromAnUninspectableOne(t *testing.T) {
	_, missingLink := os.Readlink(filepath.Join(t.TempDir(), "cwd"))
	for name, c := range map[string]struct {
		cause    error
		departed bool
		want     ownershipVerdict
	}{
		"the process is gone":                       {syscall.ESRCH, false, goneOrForeign},
		"a missing cwd link":                        {cwdLinkError(missingLink), false, goneOrForeign},
		"a zombie's missing cwd link":               {cwdLinkError(&fs.PathError{Op: "readlink", Path: "/proc/1/cwd", Err: syscall.ENOENT}), false, goneOrForeign},
		"a tool failing for a departed process":     {errors.New("exit status 1"), true, goneOrForeign},
		"an unreadable cwd link":                    {cwdLinkError(&fs.PathError{Op: "readlink", Path: "/proc/1/cwd", Err: syscall.EACCES}), false, uninspectable},
		"a missing tool":                            {exec.ErrNotFound, false, uninspectable},
		"a failing tool":                            {errors.New("exit status 1"), false, uninspectable},
		"a missing interpreter is not a departure":  {&fs.PathError{Op: "fork/exec", Path: "/x/ps", Err: syscall.ENOENT}, false, uninspectable},
		"a tool that did not answer within a bound": {context.DeadlineExceeded, false, uninspectable},
	} {
		t.Run(name, func(t *testing.T) {
			got := classifyLookFailure(c.cause, c.departed)

			assert.Equal(t, c.want, got.verdict)
			if c.want == uninspectable {
				assert.ErrorIs(t, got.cause, c.cause)
			}
		})
	}
}

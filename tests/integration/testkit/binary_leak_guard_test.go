//go:build unix

package testkit

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fakeIdentity        = "0123456789abcdef0123456789abcdef"
	otherIdentity       = "ffffffffffffffffffffffffffffffff"
	fakeSharedServerEnv = "ACDC_TESTKIT_FAKE_SHARED_SERVER"
	fakeServerLockEnv   = "ACDC_TESTKIT_FAKE_SHARED_SERVER_LOCK"
	fakeServerLocked    = "locked"
	fakeServerLogged    = "logged"
	fakeServerStartup   = "startup "
	guardTestBudget     = 30 * time.Second
	// expiringWatchdog is the guard's watchdog in tests whose outcome is a
	// report on its expiry.
	expiringWatchdog = 2 * time.Second
	// oldHandoverBound is the one-second deadline a holder once had to log
	// its startup entry by; a healthy holder may take longer.
	oldHandoverBound = time.Second
)

type recordingReporter struct{ failures []string }

func (r *recordingReporter) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// TestFakeSharedServerProcess is the body of a fake shared server, not a
// test: it holds the lock named by its environment, as production does, with
// the same library, echoes stdin lines, which proves it alive, and ends at
// stdin EOF, so it ends by itself once its test lets go of it, even a crashed
// one. Told "startup <path>", it appends its startup entry to that log.
func TestFakeSharedServerProcess(t *testing.T) {
	if os.Getenv(fakeSharedServerEnv) != "1" {
		t.Skip("runs only as the fake shared server of the leak guard tests")
	}
	if path := os.Getenv(fakeServerLockEnv); path != "" {
		locked, err := flock.New(path).TryLock()
		if err != nil || !locked {
			fmt.Fprintf(os.Stderr, "fake shared server did not take %s: %v\n", path, err)
			os.Exit(2)
		}
		fmt.Println(fakeServerLocked)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if path, ok := strings.CutPrefix(scanner.Text(), fakeServerStartup); ok {
			appendStartupEntry(path)
			continue
		}
		fmt.Println(scanner.Text())
	}
	os.Exit(0)
}

func appendStartupEntry(path string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = f.WriteString(startupEntry(os.Getpid(), time.Now()))
		err = errors.Join(err, f.Close())
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake shared server did not log its startup to %s: %v\n", path, err)
		os.Exit(2)
	}
	fmt.Println(fakeServerLogged)
}

type fakeSharedServer struct {
	pid    int
	state  *os.ProcessState
	stdin  *os.File
	stdout *os.File
	lines  *bufio.Scanner
	exited chan struct{}
}

// startFakeSharedServer starts one process, with no descendants, in its own
// process group, with args among its arguments. With a lockPath it returns
// once the process holds that lock.
func startFakeSharedServer(t *testing.T, deadline time.Time, lockPath string, args ...string) *fakeSharedServer {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestFakeSharedServerProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), fakeSharedServerEnv+"=1", fakeServerLockEnv+"="+lockPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	childIn, stdin, err := os.Pipe()
	require.NoError(t, err)
	stdout, childOut, err := os.Pipe()
	require.NoError(t, err)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = childIn, childOut, os.Stderr
	require.NoError(t, cmd.Start())
	_ = childIn.Close()
	_ = childOut.Close()
	f := &fakeSharedServer{pid: cmd.Process.Pid, stdin: stdin, stdout: stdout, lines: bufio.NewScanner(stdout), exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		f.state = cmd.ProcessState
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
	if lockPath != "" {
		f.requireLine(t, deadline, fakeServerLocked)
	}
	return f
}

func (f *fakeSharedServer) requireLine(t *testing.T, deadline time.Time, want string) {
	t.Helper()
	require.NoError(t, f.stdout.SetReadDeadline(deadline))
	require.True(t, f.lines.Scan(), "the fake shared server is gone: %v", f.lines.Err())
	require.Equal(t, want, f.lines.Text())
}

// requireAlive proves the process alive by a round trip through it.
func (f *fakeSharedServer) requireAlive(t *testing.T, deadline time.Time) {
	t.Helper()
	_, err := fmt.Fprintln(f.stdin, "ping")
	require.NoError(t, err, "the fake shared server is gone")
	f.requireLine(t, deadline, "ping")
}

// logStartup has the process log its own startup entry and returns once it
// has.
func (f *fakeSharedServer) logStartup(t *testing.T, deadline time.Time, logPath string) {
	t.Helper()
	_, err := fmt.Fprintln(f.stdin, fakeServerStartup+logPath)
	require.NoError(t, err, "the fake shared server is gone")
	f.requireLine(t, deadline, fakeServerLogged)
}

// requireEndedUnsignalled proves the process ended by itself, at stdin EOF.
func (f *fakeSharedServer) requireEndedUnsignalled(t *testing.T, deadline time.Time) {
	t.Helper()
	require.NoError(t, f.stdin.Close())
	f.requireExited(t, deadline)
	status, ok := f.state.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	assert.False(t, status.Signaled(), "the fake shared server was signalled: %v", status.Signal())
	assert.Equal(t, 0, f.state.ExitCode())
}

func (f *fakeSharedServer) requireExited(t *testing.T, deadline time.Time) {
	t.Helper()
	select {
	case <-f.exited:
	case <-time.After(time.Until(deadline)):
		t.Fatal("the fake shared server was not killed")
	}
}

// startDeadProcess returns the pid of a process that has exited and been
// reaped.
func startDeadProcess(t *testing.T, deadline time.Time) int {
	t.Helper()
	f := startFakeSharedServer(t, deadline, "")
	require.NoError(t, f.stdin.Close())
	f.requireExited(t, deadline)
	return f.pid
}

func sharedServerArgs(identity string) []string {
	return []string{"--shared-server-identity", identity, "--shared-server-linger", "1s"}
}

// guardDirs is a test's private log and endpoint directories, the lock of
// fakeIdentity at production's path among them.
type guardDirs struct {
	logDir, endpointDir, lockPath string
}

func newGuardDirs(t *testing.T) guardDirs {
	t.Helper()
	d := guardDirs{logDir: t.TempDir(), endpointDir: filepath.Join(t.TempDir(), "endpoint")}
	ep, err := endpoint.OpenEndpoint(d.endpointDir)
	require.NoError(t, err)
	d.lockPath, err = ep.LockPath(fakeIdentity)
	require.NoError(t, err)
	return d
}

// createLockFile leaves the lock file behind as a server that ended does.
func (d guardDirs) createLockFile(t *testing.T) {
	t.Helper()
	require.NoError(t, os.WriteFile(d.lockPath, nil, 0o600))
}

func (d guardDirs) logPath() string { return filepath.Join(d.logDir, fakeIdentity+".log") }

func (d guardDirs) writeLog(t *testing.T, lines ...string) {
	t.Helper()
	require.NoError(t, os.WriteFile(d.logPath(), []byte(strings.Join(lines, "")), 0o600))
}

func (d guardDirs) guard(maxLinger time.Duration) leakGuard {
	return leakGuard{logDir: d.logDir, endpointDir: d.endpointDir, maxLinger: maxLinger, grace: time.Second}
}

func (d guardDirs) check(r LeakReporter, maxLinger time.Duration) {
	checkSharedServerLeaks(r, d.guard(maxLinger), leakGuardWatchdog)
}

func (d guardDirs) checkExpiring(r LeakReporter, maxLinger time.Duration) {
	checkSharedServerLeaks(r, d.guard(maxLinger), expiringWatchdog)
}

// checkInBackground runs the guard and returns once it waits on a held lock
// it has no run to attribute to; done closes when the guard returns.
func (d guardDirs) checkInBackground(t *testing.T, deadline time.Time, r LeakReporter) (done <-chan struct{}) {
	t.Helper()
	waiting, ended := make(chan struct{}), make(chan struct{})
	g := d.guard(10 * time.Minute)
	g.onUnattributed = sync.OnceFunc(func() { close(waiting) })
	go func() {
		defer close(ended)
		checkSharedServerLeaks(r, g, leakGuardWatchdog)
	}()
	t.Cleanup(func() { <-ended })
	select {
	case <-waiting:
	case <-ended:
		t.Fatal("the leak guard returned without waiting on the held lock")
	case <-time.After(time.Until(deadline)):
		t.Fatal("the leak guard did not wait on the held lock")
	}
	return ended
}

// requireStillWaiting proves the guard has not decided an outcome after more
// than the old handover bound.
func requireStillWaiting(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("the leak guard decided an outcome before the holder logged its startup or departed")
	case <-time.After(2 * oldHandoverBound):
	}
}

func requireDone(t *testing.T, deadline time.Time, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Until(deadline)):
		t.Fatal("the leak guard did not return")
	}
}

// requireLockFree proves the guard left the lock free: it took no lock it kept.
func (d guardDirs) requireLockFree(t *testing.T) {
	t.Helper()
	lock := flock.New(d.lockPath)
	locked, err := lock.TryLock()
	require.NoError(t, err)
	require.True(t, locked, "the lock is still held")
	require.NoError(t, lock.Unlock())
}

func startupEntry(pid int, at time.Time) string {
	return fmt.Sprintf("time=%s level=INFO msg=%q version=dev pid=%d\n", at.Format(time.RFC3339Nano), sharedServerStartupMsg, pid)
}

func expiryEntry(at time.Time, linger string) string {
	return fmt.Sprintf("time=%s level=INFO msg=\"Shared server linger expired with no session attached\" linger=%s\n", at.Format(time.RFC3339Nano), linger)
}

func stoppedEntry(at time.Time) string {
	return fmt.Sprintf("time=%s level=INFO msg=\"Shared server stopped\"\n", at.Format(time.RFC3339Nano))
}

// A free lock proves no server of the log alive, whatever process its pid
// names now: even one carrying the identity is neither signalled nor, though
// past the deadline, reported.
func TestCheckSharedServerLeaks_LeavesTheLoggedProcessWhenTheLockIsFree(t *testing.T) {
	deadline := time.Now().Add(guardTestBudget)
	d := newGuardDirs(t)
	d.createLockFile(t)
	process := startFakeSharedServer(t, deadline, "", sharedServerArgs(fakeIdentity)...)
	d.writeLog(t, startupEntry(process.pid, time.Now().Add(-time.Hour)))
	reporter := &recordingReporter{}

	d.check(reporter, time.Second)

	process.requireAlive(t, deadline)
	assert.Empty(t, reporter.failures)
	d.requireLockFree(t)
}

func TestCheckSharedServerLeaks_KillsTheLockHolderOfTheLoggedPidAndIdentity(t *testing.T) {
	for name, c := range map[string]struct {
		started   time.Time
		maxLinger time.Duration
		args      []string
		overdue   bool
	}{
		"inside the largest launch linger": {time.Now().Add(-time.Minute), 10 * time.Minute, sharedServerArgs(fakeIdentity), false},
		"past the largest launch linger":   {time.Now().Add(-time.Minute), time.Second, sharedServerArgs(fakeIdentity), true},
		"its identity given inline":        {time.Now(), time.Second, []string{"--shared-server-identity=" + fakeIdentity}, false},
	} {
		t.Run(name, func(t *testing.T) {
			deadline := time.Now().Add(guardTestBudget)
			d := newGuardDirs(t)
			server := startFakeSharedServer(t, deadline, d.lockPath, c.args...)
			d.writeLog(t, startupEntry(server.pid, c.started))
			reporter := &recordingReporter{}

			d.check(reporter, c.maxLinger)

			d.requireLockFree(t)
			server.requireExited(t, deadline)
			if !c.overdue {
				assert.Empty(t, reporter.failures)
				return
			}
			require.Len(t, reporter.failures, 1)
			assert.Contains(t, reporter.failures[0], "alive after its linger deadline")
		})
	}
}

// The linger a run logged on expiry is its own and outranks the largest
// launch linger.
func TestCheckSharedServerLeaks_ReportsALiveServerPastTheLingerOfItsOwnExpiryEntry(t *testing.T) {
	deadline := time.Now().Add(guardTestBudget)
	d := newGuardDirs(t)
	server := startFakeSharedServer(t, deadline, d.lockPath, sharedServerArgs(fakeIdentity)...)
	started := time.Now().Add(-time.Minute)
	d.writeLog(t, startupEntry(server.pid, started), expiryEntry(started.Add(time.Second), "1s"))
	reporter := &recordingReporter{}

	d.check(reporter, 10*time.Minute)

	server.requireExited(t, deadline)
	require.Len(t, reporter.failures, 1)
	assert.Contains(t, reporter.failures[0], "alive after its linger deadline")
}

// A held lock is a live server: a logged pid that cannot be proven to be it
// is never signalled, and the server is reported, since it is still alive.
func TestCheckSharedServerLeaks_ReportsWithoutSignallingAHeldLockItCannotAttribute(t *testing.T) {
	for name, logged := range map[string]func(t *testing.T, deadline time.Time) (pid int, alive *fakeSharedServer){
		"the logged pid carries another identity": func(t *testing.T, deadline time.Time) (int, *fakeSharedServer) {
			p := startFakeSharedServer(t, deadline, "", sharedServerArgs(otherIdentity)...)
			return p.pid, p
		},
		"the logged pid is not a shared server": func(t *testing.T, deadline time.Time) (int, *fakeSharedServer) {
			p := startFakeSharedServer(t, deadline, "", "--x", fakeIdentity)
			return p.pid, p
		},
		"the logged pid is dead": func(t *testing.T, deadline time.Time) (int, *fakeSharedServer) {
			return startDeadProcess(t, deadline), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			deadline := time.Now().Add(guardTestBudget)
			d := newGuardDirs(t)
			holder := startFakeSharedServer(t, deadline, d.lockPath, "--holder")
			pid, process := logged(t, deadline)
			d.writeLog(t, startupEntry(pid, time.Now()))
			reporter := &recordingReporter{}

			d.check(reporter, 10*time.Minute)

			holder.requireAlive(t, deadline)
			if process != nil {
				process.requireAlive(t, deadline)
			}
			require.Len(t, reporter.failures, 1)
			assert.Contains(t, reporter.failures[0], "could not be attributed")
			assert.Contains(t, reporter.failures[0], fmt.Sprint(pid))
		})
	}
}

// A holder the log does not name yet, because every run of the log stopped
// or there is no log at all, is reported once the watchdog expires with no
// startup entry logged and the lock still held.
func TestCheckSharedServerLeaks_ReportsAHeldLockWithNoLiveRunWhenTheWatchdogExpires(t *testing.T) {
	for name, log := range map[string]func(d guardDirs, t *testing.T){
		"every run stopped": func(d guardDirs, t *testing.T) {
			d.writeLog(t, startupEntry(1, time.Now()), stoppedEntry(time.Now()))
		},
		"no log": func(guardDirs, *testing.T) {},
	} {
		t.Run(name, func(t *testing.T) {
			deadline := time.Now().Add(guardTestBudget)
			d := newGuardDirs(t)
			holder := startFakeSharedServer(t, deadline, d.lockPath, sharedServerArgs(fakeIdentity)...)
			log(d, t)
			reporter := &recordingReporter{}

			d.checkExpiring(reporter, 10*time.Minute)

			holder.requireAlive(t, deadline)
			require.Len(t, reporter.failures, 1)
			assert.Contains(t, reporter.failures[0], "could not be attributed")
		})
	}
}

// Taking the lock and logging the startup are separate steps a healthy
// holder may be descheduled between for longer than any short bound: the
// guard waits under its watchdog and kills the holder once the log names it.
func TestCheckSharedServerLeaks_KillsAHolderThatLogsItsStartupWhileTheGuardWaits(t *testing.T) {
	for name, log := range map[string]func(d guardDirs, t *testing.T){
		"after a stopped run": func(d guardDirs, t *testing.T) {
			d.writeLog(t, startupEntry(1, time.Now()), stoppedEntry(time.Now()))
		},
		"into a log yet to be created": func(guardDirs, *testing.T) {},
	} {
		t.Run(name, func(t *testing.T) {
			deadline := time.Now().Add(guardTestBudget)
			d := newGuardDirs(t)
			holder := startFakeSharedServer(t, deadline, d.lockPath, sharedServerArgs(fakeIdentity)...)
			log(d, t)
			reporter := &recordingReporter{}
			done := d.checkInBackground(t, deadline, reporter)
			requireStillWaiting(t, done)

			holder.logStartup(t, deadline, d.logPath())

			requireDone(t, deadline, done)
			holder.requireExited(t, deadline)
			d.requireLockFree(t)
			assert.Empty(t, reporter.failures)
		})
	}
}

// A holder that departs while the guard waits for its startup entry, or
// while the logged pid it could not attribute is gone, was no leak.
func TestCheckSharedServerLeaks_LeavesAHolderThatDepartsWhileTheGuardWaits(t *testing.T) {
	for name, log := range map[string]func(d guardDirs, t *testing.T, deadline time.Time){
		"no log": func(guardDirs, *testing.T, time.Time) {},
		"the logged pid exited": func(d guardDirs, t *testing.T, deadline time.Time) {
			d.writeLog(t, startupEntry(startDeadProcess(t, deadline), time.Now()))
		},
	} {
		t.Run(name, func(t *testing.T) {
			deadline := time.Now().Add(guardTestBudget)
			d := newGuardDirs(t)
			holder := startFakeSharedServer(t, deadline, d.lockPath, sharedServerArgs(fakeIdentity)...)
			log(d, t, deadline)
			reporter := &recordingReporter{}
			done := d.checkInBackground(t, deadline, reporter)
			requireStillWaiting(t, done)

			holder.requireEndedUnsignalled(t, deadline)

			requireDone(t, deadline, done)
			assert.Empty(t, reporter.failures)
		})
	}
}

func TestCheckSharedServerLeaks_ReportsALogDirectoryItCannotRead(t *testing.T) {
	deadline := time.Now().Add(guardTestBudget)
	d := newGuardDirs(t)
	holder := startFakeSharedServer(t, deadline, d.lockPath, sharedServerArgs(fakeIdentity)...)
	d.writeLog(t, startupEntry(holder.pid, time.Now()))
	require.NoError(t, os.Chmod(d.logDir, 0))
	t.Cleanup(func() { require.NoError(t, os.Chmod(d.logDir, 0o700)) })
	if _, err := os.ReadDir(d.logDir); err == nil {
		t.Skip("a mode 000 directory is readable to this user")
	}
	reporter := &recordingReporter{}

	d.check(reporter, 10*time.Minute)

	holder.requireAlive(t, deadline)
	require.NotEmpty(t, reporter.failures)
	assert.Contains(t, reporter.failures[0], d.logDir)
}

func TestCheckSharedServerLeaks_NothingToCheckWithoutLogOrEndpointDirectory(t *testing.T) {
	root := t.TempDir()
	reporter := &recordingReporter{}

	checkSharedServerLeaks(reporter, leakGuard{logDir: filepath.Join(root, "logs"), endpointDir: filepath.Join(root, "endpoint"), grace: time.Second}, leakGuardWatchdog)

	assert.Empty(t, reporter.failures)
	assert.NoDirExists(t, filepath.Join(root, "endpoint"))
}

func TestCheckSharedServerLeaks_ReportsALockItCannotTake(t *testing.T) {
	d := newGuardDirs(t)
	d.writeLog(t, startupEntry(1, time.Now()))
	reporter := &recordingReporter{}

	d.check(reporter, time.Second)

	require.Len(t, reporter.failures, 1)
	assert.Contains(t, reporter.failures[0], d.lockPath)
}

func TestCheckSharedServerLeaks_ReportsALingerExpiryAfterTheDeadline(t *testing.T) {
	d := newGuardDirs(t)
	d.createLockFile(t)
	started := time.Now().Add(-time.Hour)
	d.writeLog(t, startupEntry(1, started), expiryEntry(started.Add(time.Minute), "1s"), stoppedEntry(started.Add(time.Minute)))
	reporter := &recordingReporter{}

	d.check(reporter, 10*time.Minute)

	require.Len(t, reporter.failures, 1)
	assert.Contains(t, reporter.failures[0], "after its linger deadline")
}

func TestLaunchLinger_IsTheLingerALaunchStartsItsServerWith(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want time.Duration
	}{
		"no linger flag":     {[]string{"--shared-server"}, config.DefaultSharedServerLinger},
		"a separate value":   {[]string{"--shared-server-linger", "10m"}, 10 * time.Minute},
		"an inline value":    {[]string{"--shared-server-linger=2s"}, 2 * time.Second},
		"the last flag wins": {[]string{"--shared-server-linger", "10m", "--shared-server-linger=2s"}, 2 * time.Second},
		"an invalid value":   {[]string{"--shared-server-linger", "soon"}, config.DefaultSharedServerLinger},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, launchLinger(c.args))
		})
	}
}

func TestBinaryEnv_MaxLingerIsTheLargestOfItsLaunches(t *testing.T) {
	e := &BinaryEnv{}
	assert.Equal(t, config.DefaultSharedServerLinger, e.maxLinger())

	e.recordLaunch([]string{"--shared-server-linger", "10m"})
	e.recordLaunch([]string{"--shared-server-linger", "1s"})

	assert.Equal(t, 10*time.Minute, e.maxLinger())
}

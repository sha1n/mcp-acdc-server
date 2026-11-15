//go:build unix

package testkit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"golang.org/x/sys/unix"
)

const sharedServerStartupMsg = "Starting MCP Acdc shared server"

// ServerRun is the part of a per-identity log written by one shared server
// process. Only the lock holder writes the log, so runs never interleave.
type ServerRun struct {
	PID           int
	Started       time.Time
	LastDetach    time.Time
	LingerExpired time.Time
	Linger        time.Duration
	Stopped       time.Time
	// Sessions lists the attach and detach entries in log order, as
	// "attached:<id>" and "detached:<id>".
	Sessions []string
}

// ReadServerRuns splits a shared server log into its runs.
func ReadServerRuns(logPath string) ([]ServerRun, error) {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil, err
	}
	records, err := slogtext.Parse(data)
	if err != nil {
		return nil, err
	}
	var runs []ServerRun
	for _, record := range records {
		if record["msg"] == sharedServerStartupMsg {
			run, err := startedRun(record)
			if err != nil {
				return nil, err
			}
			runs = append(runs, run)
			continue
		}
		if len(runs) == 0 {
			continue
		}
		if err := runs[len(runs)-1].add(record); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

func startedRun(record slogtext.Record) (ServerRun, error) {
	pid, err := strconv.Atoi(record["pid"])
	if err != nil {
		return ServerRun{}, fmt.Errorf("startup entry without a pid: %v", record)
	}
	started, err := recordTime(record)
	return ServerRun{PID: pid, Started: started}, err
}

func (r *ServerRun) add(record slogtext.Record) error {
	at, err := recordTime(record)
	if err != nil {
		return err
	}
	switch record["msg"] {
	case "Session attached":
		r.Sessions = append(r.Sessions, "attached:"+record["session"])
	case "Session detached":
		r.Sessions = append(r.Sessions, "detached:"+record["session"])
		r.LastDetach = at
	case "Shared server linger expired with no session attached":
		r.LingerExpired = at
		r.Linger, err = time.ParseDuration(record["linger"])
	case "Shared server stopped":
		r.Stopped = at
	}
	return err
}

func recordTime(record slogtext.Record) (time.Time, error) {
	at, err := time.Parse(time.RFC3339Nano, record["time"])
	if err != nil {
		return time.Time{}, fmt.Errorf("log entry without a valid time: %v", record)
	}
	return at, nil
}

// lingerStart is the latest point the server's own log shows the linger
// could have been armed from.
func (r ServerRun) lingerStart() time.Time {
	if r.LastDetach.After(r.Started) {
		return r.LastDetach
	}
	return r.Started
}

// LeakReporter is the part of testing.TB the leak guard reports through.
type LeakReporter interface {
	Errorf(format string, args ...any)
}

const (
	// leakGuardWatchdog bounds one invocation of the guard, every log and
	// every wait for a lock included. It is the only real-time budget that
	// decides an outcome.
	leakGuardWatchdog = 10 * time.Second
	// logRereadTick paces rereading the log while a held lock waits for its
	// holder's startup entry: the lock's release is a kernel event, a log
	// that grew is none without a new dependency, so the log is reread as a
	// positive condition, and only the release or the watchdog ends the wait.
	logRereadTick = 20 * time.Millisecond
)

// CheckSharedServerLeaks kills every shared server of this environment that
// is still alive and reports one that outlived its linger deadline: the
// latest of its startup and last detach entries plus its linger plus grace.
// A run's linger is the one of its expiry entry, and for a run without one
// the largest any launch of this environment was started with.
func (e *BinaryEnv) CheckSharedServerLeaks(r LeakReporter) {
	checkSharedServerLeaks(r, leakGuard{logDir: e.LogDir(), endpointDir: e.EndpointDir(), maxLinger: e.maxLinger(), grace: LeakGuardGrace}, leakGuardWatchdog)
}

type leakGuard struct {
	logDir, endpointDir string
	maxLinger, grace    time.Duration
	// onUnattributed, when set, is called whenever the guard starts waiting
	// on a held lock it has no live run to attribute to.
	onUnattributed func()
}

func checkSharedServerLeaks(r LeakReporter, g leakGuard, watchdog time.Duration) {
	identities, err := g.identities()
	if err != nil || len(identities) == 0 {
		reportErr(r, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), watchdog)
	defer cancel()
	for _, identity := range identities {
		g.checkIdentity(ctx, r, identity)
	}
}

func reportErr(r LeakReporter, err error) {
	if err != nil {
		r.Errorf("leak guard: %v", err)
	}
}

// identities names every identity with a log or a lock file: a server holds
// its lock before it opens its log, so a lock file alone may be a live
// server the log does not show yet.
func (g leakGuard) identities() ([]string, error) {
	logs, err := namesWithSuffix(g.logDir, ".log")
	if err != nil {
		return nil, err
	}
	locks, err := namesWithSuffix(g.endpointDir, lockSuffix)
	if err != nil {
		return nil, err
	}
	return slices.Compact(slices.Sorted(slices.Values(append(logs, locks...)))), nil
}

// namesWithSuffix lists the names in dir ending in suffix, without it. An
// absent dir has none; every other failure to read it is an error.
func namesWithSuffix(dir, suffix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not list %s: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if name, ok := strings.CutSuffix(entry.Name(), suffix); ok {
			names = append(names, name)
		}
	}
	return names, nil
}

const lockSuffix = ".lock"

// lockPath is the lock file path production's Endpoint.LockPath names. The
// guard cannot use it: an Endpoint is obtained only by OpenEndpoint, which
// creates the directory, and the guard is an observer that creates nothing.
func (g leakGuard) lockPath(identity string) (string, error) {
	if !endpoint.ValidIdentity(identity) {
		return "", fmt.Errorf("invalid shared server identity %q", identity)
	}
	return filepath.Join(g.endpointDir, identity+lockSuffix), nil
}

// checkIdentity reads the log after trying the lock, so a run whose holder
// the try found is in what it reads, unless that holder has yet to log its
// startup.
func (g leakGuard) checkIdentity(ctx context.Context, r LeakReporter, identity string) {
	logPath := filepath.Join(g.logDir, identity+".log")
	lockPath, err := g.lockPath(identity)
	if err != nil {
		r.Errorf("leak guard: %s: %v", logPath, err)
		return
	}
	held, err := lockHeld(lockPath)
	if err != nil {
		r.Errorf("leak guard: %s: %v", logPath, err)
		return
	}
	runs, err := readRuns(logPath)
	if err != nil {
		r.Errorf("leak guard: %v", err)
		return
	}
	g.reportLateExpiries(r, logPath, runs)
	if held {
		g.checkHeldLock(ctx, r, heldLock{logPath: logPath, identity: identity, lockPath: lockPath, foundAt: time.Now()}, runs)
	}
}

// readRuns reads a log that may not exist yet: its holder opens it only after
// taking the lock.
func readRuns(logPath string) ([]ServerRun, error) {
	runs, err := ReadServerRuns(logPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", logPath, err)
	}
	return runs, nil
}

func (g leakGuard) reportLateExpiries(r LeakReporter, logPath string, runs []ServerRun) {
	for _, run := range runs {
		if !run.LingerExpired.IsZero() && run.LingerExpired.After(run.lingerStart().Add(run.Linger+g.grace)) {
			r.Errorf("leak guard: shared server %d of %s expired at %s, after its linger deadline", run.PID, logPath, run.LingerExpired.Format(time.StampMilli))
		}
	}
}

type heldLock struct {
	logPath, identity, lockPath string
	foundAt                     time.Time
}

// checkHeldLock either kills the holder, proven to be the logged pid of the
// log's last run, or, as long as the lock stays held, waits under the
// watchdog for a run it can attribute, reporting the holder once the
// watchdog expires: a held lock is a live server either way.
func (g leakGuard) checkHeldLock(ctx context.Context, r LeakReporter, h heldLock, runs []ServerRun) {
	release := awaitRelease(h.lockPath)
	ticker := time.NewTicker(logRereadTick)
	defer ticker.Stop()
	var tried runKey
	unattributed := "no run of the log is live"
	for {
		if run, ok := liveRun(runs); ok && keyOf(run) != tried {
			tried = keyOf(run)
			g.reportOverdue(r, h, run)
			err := verifyServer(run.PID, h.identity)
			if err == nil {
				killHolder(ctx, r, h, run, release)
				return
			}
			unattributed = fmt.Sprintf("its logged pid %d, left unsignalled: %v", run.PID, err)
		}
		if g.onUnattributed != nil {
			g.onUnattributed()
		}
		var done bool
		if runs, done = awaitProgress(ctx, r, h, release, ticker.C, unattributed); done {
			return
		}
	}
}

type runKey struct {
	pid     int
	started time.Time
}

func keyOf(run ServerRun) runKey { return runKey{pid: run.PID, started: run.Started} }

// awaitProgress waits for the next of the holder's departure, the watchdog's
// expiry and a tick, after which it rereads the log.
func awaitProgress(ctx context.Context, r LeakReporter, h heldLock, release *lockRelease, tick <-chan time.Time, unattributed string) ([]ServerRun, bool) {
	select {
	case <-release.done:
		if release.err != nil {
			r.Errorf("leak guard: the lock of %s is held by a shared server the leak guard could not await: %v", h.logPath, release.err)
		}
		return nil, true
	case <-ctx.Done():
		r.Errorf("leak guard: the lock of %s is held by a shared server that could not be attributed within the leak guard's watchdog: %s", h.logPath, unattributed)
		return nil, true
	case <-tick:
	}
	runs, err := readRuns(h.logPath)
	if err != nil {
		r.Errorf("leak guard: %v", err)
		return nil, true
	}
	return runs, false
}

func killHolder(ctx context.Context, r LeakReporter, h heldLock, run ServerRun, release *lockRelease) {
	if err := syscall.Kill(run.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		r.Errorf("leak guard: shared server %d of %s: kill: %v", run.PID, h.logPath, err)
		return
	}
	select {
	case <-release.done:
		if release.err != nil {
			r.Errorf("leak guard: shared server %d of %s: could not take its lock after SIGKILL: %v", run.PID, h.logPath, release.err)
		}
	case <-ctx.Done():
		r.Errorf("leak guard: shared server %d of %s did not release its lock within the leak guard's watchdog after SIGKILL: %v", run.PID, h.logPath, ctx.Err())
	}
}

func liveRun(runs []ServerRun) (ServerRun, bool) {
	if len(runs) == 0 || !runs[len(runs)-1].Stopped.IsZero() {
		return ServerRun{}, false
	}
	return runs[len(runs)-1], true
}

func (g leakGuard) reportOverdue(r LeakReporter, h heldLock, run ServerRun) {
	linger := g.maxLinger
	if !run.LingerExpired.IsZero() {
		linger = run.Linger
	}
	if deadline := run.lingerStart().Add(linger + g.grace); h.foundAt.After(deadline) {
		r.Errorf("leak guard: shared server %d of %s was alive after its linger deadline %s", run.PID, h.logPath, deadline.Format(time.StampMilli))
	}
}

func verifyServer(pid int, identity string) error {
	args, err := processArgs(pid)
	if err != nil {
		return err
	}
	if !carriesIdentity(args, identity) {
		return fmt.Errorf("its arguments %q do not carry --shared-server-identity %s", args, identity)
	}
	return nil
}

// carriesIdentity requires every identity flag in args to name identity.
func carriesIdentity(args []string, identity string) bool {
	found := false
	for i, arg := range args {
		value, inline := strings.CutPrefix(arg, "--shared-server-identity=")
		if !inline && arg == "--shared-server-identity" {
			value, inline = "", true
			if i+1 < len(args) {
				value = args[i+1]
			}
		}
		if inline && value != identity {
			return false
		}
		found = found || inline
	}
	return found
}

// lockHeld tries the lock without blocking; closing the descriptor releases
// a lock the try took.
func lockHeld(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("could not try the lock: %w", err)
	}
	defer func() { _ = f.Close() }()
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("could not try the lock %s: %w", path, err)
	}
	return false, nil
}

// lockRelease is a blocking take of the lock, done once the holder is gone.
type lockRelease struct {
	done chan struct{}
	err  error
}

// awaitRelease blocks on a descriptor of its own, which it closes the moment
// the call returns: a pending flock cannot be cancelled, and closing its
// descriptor from another goroutine blocks on macOS. A wait the guard stops
// waiting for therefore ends with the holder, and the lock it is then
// granted is released at once.
func awaitRelease(path string) *lockRelease {
	l := &lockRelease{done: make(chan struct{})}
	f, err := os.Open(path)
	if err != nil {
		l.err = err
		close(l.done)
		return l
	}
	go func() {
		defer close(l.done)
		l.err = flockRetryingEINTR(int(f.Fd()))
		_ = f.Close()
	}()
	return l
}

func flockRetryingEINTR(fd int) error {
	for {
		if err := unix.Flock(fd, unix.LOCK_EX); !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}

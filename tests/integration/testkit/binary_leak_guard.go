//go:build unix

package testkit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
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

// leakGuardWatchdog bounds the guard's work on one server run, every
// inspection tool it runs included, so a hung tool cannot hang the test.
const (
	leakGuardWatchdog   = 10 * time.Second
	inspectionWaitDelay = 500 * time.Millisecond
)

// CheckSharedServerLeaks kills every shared server logged under logDir that is
// still alive and reports one that outlived its linger deadline: the latest
// of its startup and last detach entries plus its linger plus grace.
// launchDir is the working directory of the launches that spawned them.
func CheckSharedServerLeaks(r LeakReporter, logDir, launchDir string, grace time.Duration) {
	checkSharedServerLeaks(r, logDir, launchDir, grace, leakGuardWatchdog)
}

func checkSharedServerLeaks(r LeakReporter, logDir, launchDir string, grace, watchdog time.Duration) {
	resolvedLaunchDir, err := filepath.EvalSymlinks(launchDir)
	if err != nil {
		r.Errorf("leak guard: %v", err)
		return
	}
	logs, err := filepath.Glob(filepath.Join(logDir, "*.log"))
	if err != nil {
		r.Errorf("leak guard: %v", err)
		return
	}
	for _, logPath := range logs {
		runs, err := ReadServerRuns(logPath)
		if err != nil {
			r.Errorf("leak guard: failed to read %s: %v", logPath, err)
			continue
		}
		owner := serverOwner{identity: strings.TrimSuffix(filepath.Base(logPath), ".log"), launchDir: resolvedLaunchDir}
		for _, run := range runs {
			ctx, cancel := context.WithTimeout(context.Background(), watchdog)
			checkServerRun(ctx, r, logPath, owner, run, grace)
			cancel()
		}
	}
}

func checkServerRun(ctx context.Context, r LeakReporter, logPath string, owner serverOwner, run ServerRun, grace time.Duration) {
	if !run.LingerExpired.IsZero() && run.LingerExpired.After(run.lingerStart().Add(run.Linger+grace)) {
		r.Errorf("leak guard: shared server %d of %s expired at %s, after its linger deadline", run.PID, logPath, run.LingerExpired.Format(time.StampMilli))
	}
	if !run.Stopped.IsZero() {
		return
	}
	look := owner.look(ctx, run.PID)
	switch look.verdict {
	case goneOrForeign:
		return
	case uninspectable:
		r.Errorf("leak guard: shared server %d of %s was left unsignalled: %v", run.PID, logPath, look.cause)
		return
	}
	if err := owner.killAndAwaitExit(ctx, run.PID); err != nil {
		r.Errorf("leak guard: shared server %d of %s: %v", run.PID, logPath, err)
	}
	if deadline := run.lingerStart().Add(look.linger + grace); time.Now().After(deadline) {
		r.Errorf("leak guard: shared server %d of %s was alive after its linger deadline %s", run.PID, logPath, deadline.Format(time.StampMilli))
	}
}

// serverOwner is what proves a pid is the server of one log, so a reused pid
// is not signalled: the log's identity is the identity among its arguments,
// and its working directory, inherited from the spawning launch, is the
// launch directory, which belongs to one test for as long as it exists.
type serverOwner struct {
	identity  string
	launchDir string
}

type ownershipVerdict int

const (
	// goneOrForeign: the pid no longer names this log's server (exited,
	// reused, or a zombie); it is neither signalled nor reported.
	goneOrForeign ownershipVerdict = iota
	owned
	// uninspectable: ownership was neither proven nor disproven, so the pid
	// is not signalled, and the guard reports it, since a silent pass here
	// could hide a leaked server.
	uninspectable
)

// ownership is what one look at a pid proved; cause is set only when the
// verdict is uninspectable, and linger only when it is owned.
type ownership struct {
	verdict ownershipVerdict
	linger  time.Duration
	cause   error
}

// errDeparted marks an inspection failure that itself proves the process gone.
var errDeparted = errors.New("the process is gone")

func classifyLookFailure(cause error, departed bool) ownership {
	if departed || errors.Is(cause, errDeparted) || errors.Is(cause, syscall.ESRCH) {
		return ownership{verdict: goneOrForeign}
	}
	return ownership{verdict: uninspectable, cause: cause}
}

// look reads the linger from the live process's own arguments, which are its
// spawning launch's.
func (o serverOwner) look(ctx context.Context, pid int) ownership {
	if syscall.Kill(pid, 0) != nil {
		return ownership{verdict: goneOrForeign}
	}
	stat, args, err := processStatus(ctx, pid)
	if err != nil {
		return o.failedLook(ctx, pid, err)
	}
	if isZombie(stat) || !carriesIdentity(args, o.identity) {
		return ownership{verdict: goneOrForeign}
	}
	dir, err := processDir(ctx, pid)
	if err != nil {
		return o.failedLook(ctx, pid, err)
	}
	if dir != o.launchDir {
		return ownership{verdict: goneOrForeign}
	}
	return ownership{verdict: owned, linger: lingerArg(args)}
}

func (o serverOwner) failedLook(ctx context.Context, pid int, cause error) ownership {
	look := classifyLookFailure(cause, departed(ctx, pid))
	if look.verdict == uninspectable {
		look.cause = fmt.Errorf("could not inspect process %d for identity %s: %w", pid, o.identity, cause)
	}
	return look
}

// departed tells whether a process ended while it was being inspected, so
// that the failure of a look is not mistaken for a failure to inspect it.
func departed(ctx context.Context, pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return true
	}
	stat, _, err := processStatus(ctx, pid)
	return err == nil && isZombie(stat)
}

func isZombie(stat string) bool {
	return strings.HasPrefix(stat, "Z")
}

func processStatus(ctx context.Context, pid int) (string, []string, error) {
	out, err := runInspection(ctx, "ps", "-ww", "-o", "stat=,args=", "-p", strconv.Itoa(pid))
	if err != nil {
		return "", nil, err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", nil, fmt.Errorf("ps listed no process %d", pid)
	}
	return fields[0], fields[1:], nil
}

// runInspection runs a tool under the guard's watchdog; WaitDelay also bounds
// the wait for output a killed tool's descendant may still hold open.
func runInspection(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = inspectionWaitDelay
	out, err := cmd.Output()
	if err != nil && ctx.Err() != nil {
		return "", fmt.Errorf("%s did not answer within the leak guard's watchdog: %w", name, ctx.Err())
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
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

// processDir reads a process's working directory: /proc on Linux, where lsof
// may be missing, and lsof elsewhere, where /proc does not exist.
func processDir(ctx context.Context, pid int) (string, error) {
	if runtime.GOOS == "linux" {
		dir, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
		return dir, cwdLinkError(err)
	}
	out, err := runInspection(ctx, "lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn")
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(out) {
		if dir, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "n"); ok {
			return dir, nil
		}
	}
	return "", fmt.Errorf("lsof reported no working directory for %d", pid)
}

// cwdLinkError marks a missing /proc/<pid>/cwd link as a departure: the
// process exited, or is a zombie, whose cwd link the kernel removes. Any
// other error, a permission error above all, leaves ownership unknown.
func cwdLinkError(err error) error {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("%w: %w", errDeparted, err)
	}
	return err
}

func containsArg(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

func lingerArg(args []string) time.Duration {
	linger := config.DefaultSharedServerLinger
	for i, arg := range args {
		value, inline := strings.CutPrefix(arg, "--shared-server-linger=")
		if !inline && arg == "--shared-server-linger" && i+1 < len(args) {
			value, inline = args[i+1], true
		}
		if parsed, err := time.ParseDuration(value); inline && err == nil {
			linger = parsed
		}
	}
	return linger
}

// killAndAwaitExit proves ownership again right before the signal and on
// every look while waiting, since the pid may be reused at any point.
func (o serverOwner) killAndAwaitExit(ctx context.Context, pid int) error {
	if look := o.look(ctx, pid); look.verdict != owned {
		return look.cause
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("still alive when the leak guard's watchdog expired after SIGKILL: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
		if look := o.look(ctx, pid); look.verdict != owned {
			return look.cause
		}
	}
}

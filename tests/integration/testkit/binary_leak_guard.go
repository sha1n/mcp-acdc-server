//go:build unix

package testkit

import (
	"errors"
	"fmt"
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

// CheckSharedServerLeaks kills every shared server logged under logDir that is
// still alive and reports one that outlived its linger deadline: the latest
// of its startup and last detach entries plus its linger plus grace.
// launchDir is the working directory of the launches that spawned them.
func CheckSharedServerLeaks(r LeakReporter, logDir, launchDir string, grace time.Duration) {
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
			checkServerRun(r, logPath, owner, run, grace)
		}
	}
}

func checkServerRun(r LeakReporter, logPath string, owner serverOwner, run ServerRun, grace time.Duration) {
	if !run.LingerExpired.IsZero() && run.LingerExpired.After(run.lingerStart().Add(run.Linger+grace)) {
		r.Errorf("leak guard: shared server %d of %s expired at %s, after its linger deadline", run.PID, logPath, run.LingerExpired.Format(time.StampMilli))
	}
	if !run.Stopped.IsZero() {
		return
	}
	linger, alive := owner.liveServerLinger(run.PID)
	if !alive {
		return
	}
	if err := owner.killAndAwaitExit(run.PID); err != nil {
		r.Errorf("leak guard: shared server %d of %s: %v", run.PID, logPath, err)
	}
	if deadline := run.lingerStart().Add(linger + grace); time.Now().After(deadline) {
		r.Errorf("leak guard: shared server %d of %s was alive after its linger deadline %s", run.PID, logPath, deadline.Format(time.StampMilli))
	}
}

// serverOwner is what proves a pid is the server of one log, so a reused pid
// is never signalled: the log's identity is the identity among its arguments,
// and its working directory, inherited from the spawning launch, is the
// launch directory, which belongs to one test for as long as it exists.
type serverOwner struct {
	identity  string
	launchDir string
}

// liveServerLinger reads the linger from the live process's own arguments,
// which are its spawning launch's. A pid that no longer names this log's
// server (exited, reused, or a zombie) is a server that is gone.
func (o serverOwner) liveServerLinger(pid int) (time.Duration, bool) {
	if syscall.Kill(pid, 0) != nil {
		return 0, false
	}
	out, err := exec.Command("ps", "-ww", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, false
	}
	args := strings.Fields(string(out))
	if !carriesIdentity(args, o.identity) {
		return 0, false
	}
	if dir, err := processDir(pid); err != nil || dir != o.launchDir {
		return 0, false
	}
	return lingerArg(args), true
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
func processDir(pid int) (string, error) {
	if runtime.GOOS == "linux" {
		return os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	}
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(out)) {
		if dir, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "n"); ok {
			return dir, nil
		}
	}
	return "", fmt.Errorf("lsof reported no working directory for %d", pid)
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
func (o serverOwner) killAndAwaitExit(pid int) error {
	if _, owned := o.liveServerLinger(pid); !owned {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, owned := o.liveServerLinger(pid); !owned {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("still alive 10s after SIGKILL")
}

//go:build unix

package testkit

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
)

// SignalSharedServer sends sig to the shared server of run only once it is
// proven to be that server the way the leak guard proves it: the log is this
// environment's, the identity's lock is held, run is the log's last run and
// has not stopped, and the process's arguments carry the identity. A pid of a
// stale run is therefore never signalled.
func (e *BinaryEnv) SignalSharedServer(logPath string, run ServerRun, sig syscall.Signal) error {
	return signalOwnedServer(e.LogDir(), e.EndpointDir(), logPath, run, sig)
}

func signalOwnedServer(logDir, endpointDir, logPath string, run ServerRun, sig syscall.Signal) error {
	if filepath.Dir(logPath) != logDir {
		return fmt.Errorf("%s is not a log of this environment", logPath)
	}
	identity := strings.TrimSuffix(filepath.Base(logPath), ".log")
	if err := requireLiveRun(endpointDir, logPath, identity, run); err != nil {
		return err
	}
	if err := verifyServer(run.PID, identity); err != nil {
		return fmt.Errorf("shared server %d of %s: %w", run.PID, logPath, err)
	}
	return syscall.Kill(run.PID, sig)
}

// requireLiveRun tries the lock before reading the log, so the holder the
// try found has its startup entry in what is read.
func requireLiveRun(endpointDir, logPath, identity string, run ServerRun) error {
	lockPath, err := leakGuard{endpointDir: endpointDir}.lockPath(identity)
	if err != nil {
		return err
	}
	held, err := lockHeld(lockPath)
	if err != nil {
		return err
	}
	if !held {
		return errors.New("no shared server holds " + lockPath)
	}
	runs, err := ReadServerRuns(logPath)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return fmt.Errorf("%s has no run", logPath)
	}
	last := runs[len(runs)-1]
	if last.PID != run.PID || !last.Started.Equal(run.Started) || !last.Stopped.IsZero() {
		return fmt.Errorf("shared server %d of %s is not the live run of its log", run.PID, logPath)
	}
	return nil
}

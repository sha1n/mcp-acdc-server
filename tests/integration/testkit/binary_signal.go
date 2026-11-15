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
// proven to be that server by the leak guard's own attribution: the log is
// this environment's, the identity's lock is held, run is the log's last run
// and has not stopped, and the process's arguments carry the identity. A pid
// of a stale run is therefore never signalled.
func (e *BinaryEnv) SignalSharedServer(logPath string, run ServerRun, sig syscall.Signal) error {
	return signalOwnedServer(e.LogDir(), e.EndpointDir(), logPath, run, sig)
}

func signalOwnedServer(logDir, endpointDir, logPath string, run ServerRun, sig syscall.Signal) error {
	identity, isLog := strings.CutSuffix(filepath.Base(logPath), ".log")
	if filepath.Dir(logPath) != logDir || !isLog {
		return fmt.Errorf("%s is not a log of this environment", logPath)
	}
	o, err := leakGuard{logDir: logDir, endpointDir: endpointDir}.observe(identity)
	if err != nil {
		return err
	}
	if !o.held {
		return errors.New("no shared server holds " + o.lockPath)
	}
	live, ok, err := o.attribute(o.runs, runKey{})
	if !ok || keyOf(live) != keyOf(run) {
		return fmt.Errorf("shared server %d of %s is not the live run of its log", run.PID, logPath)
	}
	if err != nil {
		return fmt.Errorf("shared server %d of %s: %w", run.PID, logPath, err)
	}
	return syscall.Kill(run.PID, sig)
}

//go:build linux || darwin

package testkit

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (d guardDirs) signal(t *testing.T, logPath string, sig syscall.Signal) error {
	t.Helper()
	runs, err := ReadServerRuns(logPath)
	require.NoError(t, err)
	return signalOwnedServer(d.logDir, d.endpointDir, logPath, runs[0], sig)
}

func TestSignalOwnedServer_SignalsTheLockHolderOfTheLogsLiveRun(t *testing.T) {
	deadline := time.Now().Add(guardTestBudget)
	d := newGuardDirs(t)
	server := startFakeSharedServer(t, deadline, d.lockPath, sharedServerArgs(fakeIdentity)...)
	d.writeLog(t, startupEntry(server.pid, time.Now()))

	require.NoError(t, d.signal(t, d.logPath(), syscall.SIGKILL))

	server.requireExited(t, deadline)
	status, ok := server.state.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	assert.Equal(t, syscall.SIGKILL, status.Signal())
}

func TestSignalOwnedServer_LeavesAProcessItCannotProveIsTheLogsLiveServer(t *testing.T) {
	for name, c := range map[string]struct {
		holdsLock bool
		args      []string
		laterLog  func(pid int) string
		logDir    func(d guardDirs) string
		want      string
	}{
		"the lock is free": {
			args: sharedServerArgs(fakeIdentity), want: "no shared server holds",
		},
		"another identity in its arguments": {
			holdsLock: true, args: sharedServerArgs(otherIdentity), want: "do not carry --shared-server-identity",
		},
		"a later run in the log": {
			holdsLock: true, args: sharedServerArgs(fakeIdentity), want: "is not the live run of its log",
			laterLog: func(pid int) string { return startupEntry(pid+1, time.Now()) },
		},
		"its run has stopped": {
			holdsLock: true, args: sharedServerArgs(fakeIdentity), want: "is not the live run of its log",
			laterLog: func(int) string { return stoppedEntry(time.Now()) },
		},
		"a log of another environment": {
			holdsLock: true, args: sharedServerArgs(fakeIdentity), want: "is not a log of this environment",
			logDir: func(d guardDirs) string { return filepath.Join(d.logDir, "other") },
		},
	} {
		t.Run(name, func(t *testing.T) {
			deadline := time.Now().Add(guardTestBudget)
			d := newGuardDirs(t)
			lockPath := ""
			if c.holdsLock {
				lockPath = d.lockPath
			} else {
				d.createLockFile(t)
			}
			process := startFakeSharedServer(t, deadline, lockPath, c.args...)
			entries := []string{startupEntry(process.pid, time.Now())}
			if c.laterLog != nil {
				entries = append(entries, c.laterLog(process.pid))
			}
			d.writeLog(t, entries...)
			logPath := d.logPath()
			if c.logDir != nil {
				d.logDir = c.logDir(d)
			}

			err := d.signal(t, logPath, syscall.SIGKILL)

			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
			process.requireAlive(t, deadline)
		})
	}
}

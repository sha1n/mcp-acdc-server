//go:build unix

package sharedserver

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lastLogLine(logs string) string {
	lines := strings.Split(strings.TrimRight(logs, "\n"), "\n")
	return lines[len(lines)-1]
}

func TestRun_LingerExitLogsTheExpiryOnceAndTheStoppedEntryLast(t *testing.T) {
	logDir := t.TempDir()
	build, observed := observingBuild("build marker", nil)
	run := startExplicitLoggerRun(t, Config{
		Identity:    testIdentity,
		EndpointDir: shortTempDir(t),
		LogDir:      logDir,
		Build:       build,
		Linger:      config.MinSharedServerLinger,
	})
	awaitExplicitLoggerBuild(t, observed)

	select {
	case err := <-run.done:
		run.done <- err
		require.NoError(t, err)
	case <-time.After(serveTestDeadline):
		t.Fatal("Run did not exit after the linger")
	}
	logs := readExplicitLoggerLog(t, filepath.Join(logDir, testIdentity+".log"))

	expiry := linesMatching(logs, `^time=\S+ level=INFO msg="Shared server linger expired with no session attached" linger=1s$`)
	assert.Len(t, expiry, 1, logs)
	assert.Len(t, linesMatching(logs, `msg="Shared server stopped"`), 1, logs)
	assert.Regexp(t, `^time=\S+ level=INFO msg="Shared server stopped"$`, lastLogLine(logs))
	assert.NotContains(t, logs, "session=")
}

func TestRun_SignalStopLogsTheStoppedEntryLastWithoutALingerExpiry(t *testing.T) {
	logDir := t.TempDir()
	build, observed := observingBuild("build marker", nil)
	run := startExplicitLoggerRun(t, Config{
		Identity:    testIdentity,
		EndpointDir: shortTempDir(t),
		LogDir:      logDir,
		Build:       build,
		Linger:      time.Hour,
	})
	awaitExplicitLoggerBuild(t, observed)

	run.stop(t)
	logs := readExplicitLoggerLog(t, filepath.Join(logDir, testIdentity+".log"))

	assert.Empty(t, linesMatching(logs, `linger expired`), logs)
	assert.Len(t, linesMatching(logs, `msg="Shared server stopped"`), 1, logs)
	assert.Regexp(t, `^time=\S+ level=INFO msg="Shared server stopped"$`, lastLogLine(logs))
	assert.NotContains(t, logs, "session=")
}

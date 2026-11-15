//go:build unix

package sharedserver

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

const (
	connectionLostErrorFormat = `{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"shared server connection lost before a response was received"}}` + "\n"
	crashWarningMessage       = "Shared server connection lost, recovering"
	recoveredMessage          = "Recovered the shared server connection"
	attachMessage             = "Attached to the shared server"
	sessionAttachedMessage    = "Session attached"
)

// recoveryLaunchArgs returns the arguments of a shared-mode launch whose
// shared servers, the first and every one a recovery starts, outlive the
// test: none of these tests is about the linger, and a linger that expires
// before a recovering proxy dials would turn one recovery into two.
func recoveryLaunchArgs(contentDir string, extra ...string) []string {
	args := testkit.SharedLaunchArgs(contentDir,
		"--shared-server-linger", "1h",
		"--shared-server-connect-wait", "20s",
	)
	return append(args, extra...)
}

// modernSearch is a modern-era search request: it carries its protocol
// metadata, so it needs no initialize handshake.
func modernSearch(id, query string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{%s,"name":"search","arguments":{"query":%q}}}`,
		id, modernMeta, query)
}

func connectionLostError(id string) string {
	return fmt.Sprintf(connectionLostErrorFormat, id)
}

func stderrRecords(t *testing.T, l *testkit.LaunchUnderTest, match slogtext.Record) []slogtext.Record {
	t.Helper()
	records, err := slogtext.Parse(completeLines([]byte(l.Stderr())))
	require.NoError(t, err)
	return slogtext.Filter(records, match)
}

// awaitStderrRecords returns the records matching match once the launch has
// written at least n of them, failing the test at deadline.
func awaitStderrRecords(t *testing.T, l *testkit.LaunchUnderTest, match slogtext.Record, n int, deadline time.Time) []slogtext.Record {
	t.Helper()
	for {
		if got := stderrRecords(t, l, match); len(got) >= n {
			return got
		}
		require.True(t, time.Now().Before(deadline), "the launch did not write %d %q records: %s", n, match["msg"], l.Stderr())
		time.Sleep(lossPollInterval)
	}
}

func attachRecords(t *testing.T, l *testkit.LaunchUnderTest, n int, deadline time.Time) []slogtext.Record {
	t.Helper()
	return awaitStderrRecords(t, l, slogtext.Record{"level": "INFO", "msg": attachMessage}, n, deadline)
}

func serverLogRecords(t *testing.T, path string, match slogtext.Record) []slogtext.Record {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	records, err := slogtext.Parse(completeLines(data))
	require.NoError(t, err)
	return slogtext.Filter(records, match)
}

// awaitServerLogRecords returns the records of the shared server log at path
// matching match once it holds at least n of them, failing the test at
// deadline.
func awaitServerLogRecords(t *testing.T, path string, match slogtext.Record, n int, deadline time.Time) []slogtext.Record {
	t.Helper()
	for {
		if got := serverLogRecords(t, path, match); len(got) >= n {
			return got
		}
		require.True(t, time.Now().Before(deadline), "the shared server log %s does not hold %d %q records", path, n, match["msg"])
		time.Sleep(lossPollInterval)
	}
}

// sessionsOf returns the session attribute of each record, in order.
func sessionsOf(records []slogtext.Record) []string {
	sessions := make([]string, len(records))
	for i, r := range records {
		sessions[i] = r["session"]
	}
	return sessions
}

// requireDistinct fails the test unless every value differs from the others.
func requireDistinct(t *testing.T, values []string, what string) {
	t.Helper()
	seen := map[string]bool{}
	for _, v := range values {
		require.NotEmpty(t, v, what)
		require.False(t, seen[v], "%s repeats %q: %v", what, v, values)
		seen[v] = true
	}
}

func requireNoProxyRecordsInServerLog(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, msg := range []string{crashWarningMessage, recoveredMessage, attachMessage} {
		require.NotContains(t, string(data), fmt.Sprintf("msg=%q", msg), "a session proxy record reached the shared server log")
	}
}

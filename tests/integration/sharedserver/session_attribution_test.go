//go:build unix

package sharedserver

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	attributionInitialize  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"attribution-test","version":"1.0.0"}}}`
	attributionInitialized = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	attributionSearch      = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"query":"authentication"}}}`
	attributionLateDoc     = "---\nname: Late Guide\ndescription: Added while the shared server runs\n---\nThe unique term latecomer lives here.\n"
)

var (
	attachEntry      = regexp.MustCompile(`(?m)^time=\S+ level=INFO msg="Session attached" session=([0-9a-f]{8})$`)
	proxyAttachLine  = regexp.MustCompile(`(?m)^time=\S+ level=INFO msg=.* session=([0-9a-f]{8}) log=(\S+)$`)
	proxyNoLogLine   = regexp.MustCompile(`(?m)^time=\S+ level=WARN msg=.* session=([0-9a-f]{8})$`)
	anySessionAttrRe = regexp.MustCompile(`\bsession=`)
)

func readSharedServerLog(t *testing.T, h *testkit.SharedServerHarness, id string) string {
	t.Helper()
	logs, err := os.ReadFile(filepath.Join(h.LogDir, id+".log"))
	require.NoError(t, err)
	return string(logs)
}

func logLinesContaining(logs, fragment string) []string {
	var matched []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, fragment) {
			matched = append(matched, line)
		}
	}
	return matched
}

func attachedSessions(logs string) []string {
	var sessions []string
	for _, match := range attachEntry.FindAllStringSubmatch(logs, -1) {
		sessions = append(sessions, match[1])
	}
	return sessions
}

func proxyAttachedSession(t *testing.T, l *testkit.LaunchUnderTest) string {
	t.Helper()
	matches := proxyAttachLine.FindAllStringSubmatch(l.Stderr(), -1)
	require.Len(t, matches, 1, "exactly one attach line on the session proxy's stderr:\n%s", l.Stderr())
	return matches[0][1]
}

func awaitBuiltCandidateExit(h *testkit.SharedServerHarness) {
	h.Eventually(func() bool {
		for _, exit := range h.SharedServerExits() {
			if exit.Built {
				return true
			}
		}
		return false
	}, "the shared server exits after the linger")
}

func TestSessionAttribution_StartupRaceInitialisesEveryProxyWithOneStartupAndDistinctSessions(t *testing.T) {
	const proxies = 8
	// A linger nobody can outwait: no proxy can lose the first second after
	// bind, so every attach reaches the one shared server.
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t), "--shared-server-linger", "1h")
	h := testkit.NewSharedServerHarness(t)
	id := h.Identity(args)
	launches := make([]*testkit.LaunchUnderTest, proxies)
	for i := range launches {
		launches[i] = h.Launch(args...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()
	sessions := make([]*mcp.ClientSession, proxies)
	for i, l := range launches {
		sessions[i] = l.Connect(ctx)
	}
	for _, session := range sessions {
		_, err := session.ListTools(ctx, nil)
		require.NoError(t, err)
	}
	logs := readSharedServerLog(t, h, id)

	assert.Len(t, logLinesContaining(logs, `msg="Starting MCP Acdc shared server"`), 1, logs)
	attached := attachedSessions(logs)
	assert.Len(t, attached, proxies, logs)
	distinct := map[string]bool{}
	for _, session := range attached {
		distinct[session] = true
	}
	assert.Len(t, distinct, proxies, "every session gets its own identifier: %v", attached)
	assert.Equal(t, 1, h.Builds())
}

func TestSessionAttribution_RequestEntriesCarryTheirSessionAndServerEntriesNone(t *testing.T) {
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"auth-guide.md": "---\nname: Authentication Guide\ndescription: Guide for authentication\n---\nThis document covers authentication.\n",
		},
	})
	args := testkit.SharedLaunchArgs(contentDir)
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()

	var a, b *testkit.LaunchUnderTest
	var bSession *mcp.ClientSession
	h := runWinningColdStart(t, func(t *testing.T, h *testkit.SharedServerHarness) {
		a = h.Launch(args...)
		a.WriteLine(attributionInitialize)
		a.WriteLine(attributionInitialized)
		a.WriteLine(attributionSearch)
		a.ReadLine()
		assert.Contains(t, string(a.ReadLine()), "Authentication Guide")
		b = h.Launch(args...)
		bSession = connectAndListTools(t, b)
	})
	id := h.Identity(args)
	sessionA := proxyAttachedSession(t, a)
	sessionB := proxyAttachedSession(t, b)
	require.NotEqual(t, sessionA, sessionB)

	resourcesDir := content.NewContentProvider(contentDir).ResourcesDir
	require.NoError(t, os.WriteFile(filepath.Join(resourcesDir, "late-guide.md"), []byte(attributionLateDoc), 0o600))
	h.Eventually(func() bool {
		result, err := bSession.CallTool(ctx, &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "latecomer"}})
		return err == nil && !result.IsError && len(result.Content) > 0 &&
			strings.Contains(result.Content[0].(*mcp.TextContent).Text, "Late Guide")
	}, "a search by session B refreshes the catalog")
	a.CloseStdin()
	require.NoError(t, bSession.Close())
	b.CloseStdin()
	awaitBuiltCandidateExit(h)
	logs := readSharedServerLog(t, h, id)

	assert.Len(t, logLinesContaining(logs, `msg="Search request" session=`+sessionA+` query=authentication`), 1, logs)
	assert.NotEmpty(t, logLinesContaining(logs, `msg="Search request" session=`+sessionB+` query=latecomer`), logs)
	for _, line := range logLinesContaining(logs, `msg="Search request"`) {
		assert.Regexp(t, anySessionAttrRe, line, "a request entry without its session")
	}
	for _, session := range []string{sessionA, sessionB} {
		assert.Len(t, logLinesContaining(logs, `level=INFO msg="Session attached" session=`+session), 1, logs)
		assert.Len(t, logLinesContaining(logs, `level=INFO msg="Session detached" session=`+session), 1, logs)
	}
	unattributed := map[string]string{
		"startup":     `msg="Starting MCP Acdc shared server"`,
		"index build": `msg="Registered tool"`,
		"refresh":     `msg="Catalog refresh republished catalog"`,
		"linger":      `linger expired`,
		"shutdown":    `msg="Shared server stopped"`,
	}
	for kind, fragment := range unattributed {
		lines := logLinesContaining(logs, fragment)
		assert.NotEmpty(t, lines, "%s entry missing:\n%s", kind, logs)
		for _, line := range lines {
			assert.NotRegexp(t, anySessionAttrRe, line, "%s entry carries a session", kind)
		}
	}
	for _, line := range logLinesContaining(logs, "session=") {
		assert.Regexp(t, `session=(`+sessionA+`|`+sessionB+`)\b`, line)
	}
}

func TestSessionAttribution_ProxyStderrNamesTheReadyLineSessionAndTheLogPath(t *testing.T) {
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	var l *testkit.LaunchUnderTest
	h := runWinningColdStart(t, func(t *testing.T, h *testkit.SharedServerHarness) {
		l = h.Launch(args...)
		connectAndListTools(t, l)
	})
	id := h.Identity(args)
	logs := readSharedServerLog(t, h, id)
	attached := attachedSessions(logs)
	require.Len(t, attached, 1, logs)

	matches := proxyAttachLine.FindAllStringSubmatch(l.Stderr(), -1)
	require.Len(t, matches, 1, l.Stderr())
	assert.Equal(t, attached[0], matches[0][1])
	logPath := matches[0][2]
	assert.True(t, filepath.IsAbs(logPath), logPath)
	reported, err := os.Stat(logPath)
	require.NoError(t, err)
	expected, err := os.Stat(filepath.Join(h.LogDir, id+".log"))
	require.NoError(t, err)
	assert.True(t, os.SameFile(expected, reported), "the attach line names %s", logPath)
	assert.Empty(t, proxyNoLogLine.FindAllString(l.Stderr(), -1), l.Stderr())
}

func TestSessionAttribution_ProxyStderrWarnsWhenTheSharedServerHasNoLogFile(t *testing.T) {
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	var l *testkit.LaunchUnderTest
	runWinningColdStart(t, func(t *testing.T, h *testkit.SharedServerHarness) {
		readOnly := t.TempDir()
		require.NoError(t, os.Chmod(readOnly, 0o500))
		t.Cleanup(func() {
			if err := os.Chmod(readOnly, 0o700); err != nil {
				t.Errorf("failed to restore %s: %v", readOnly, err)
			}
		})
		h.LogDir = filepath.Join(readOnly, "logs")
		l = h.Launch(args...)
		connectAndListTools(t, l)
	})

	assert.Len(t, proxyNoLogLine.FindAllString(l.Stderr(), -1), 1, l.Stderr())
	assert.Empty(t, proxyAttachLine.FindAllString(l.Stderr(), -1), l.Stderr())
}

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
	handlerAttributionKeptURI     = "acdc://auth-guide"
	handlerAttributionDoomedURI   = "acdc://doomed-guide"
	handlerAttributionMissingURI  = "acdc://no-such-guide"
	handlerAttributionPrompt      = "greet"
	handlerAttributionPromptArg   = "who"
	handlerAttributionKeptDoc     = "---\nname: Authentication Guide\ndescription: Guide for authentication\n---\nThis document covers authentication.\n"
	handlerAttributionDoomedDoc   = "---\nname: Doomed Guide\ndescription: Deleted while the shared server runs\n---\nThis document disappears.\n"
	handlerAttributionPromptDoc   = "---\nname: greet\ndescription: Greeting prompt\narguments:\n  - name: who\n    description: who to greet\n    required: true\n---\nHello {{.who}}"
	handlerAttributionUnsearchLen = 5000
)

type handlerAttributionEntry struct {
	name    string
	level   string
	msg     string
	attrs   string
	errored bool
	count   int
}

func (e handlerAttributionEntry) pattern(session string) *regexp.Regexp {
	expr := `^time=\S+ level=` + e.level + ` msg="` + regexp.QuoteMeta(e.msg) + `" session=` + session + ` ` + regexp.QuoteMeta(e.attrs)
	if e.errored {
		expr += ` error=".+"`
	}
	return regexp.MustCompile(expr + `$`)
}

func handlerAttributionLinesMatching(logs string, re *regexp.Regexp) []string {
	var matched []string
	for _, line := range strings.Split(logs, "\n") {
		if re.MatchString(line) {
			matched = append(matched, line)
		}
	}
	return matched
}

func requireToolCallFailed(t *testing.T, ctx context.Context, session *mcp.ClientSession, params *mcp.CallToolParams) {
	t.Helper()
	result, err := session.CallTool(ctx, params)
	require.True(t, err != nil || result.IsError, "the %s call must fail", params.Name)
}

func requireToolCallSucceeded(t *testing.T, ctx context.Context, session *mcp.ClientSession, params *mcp.CallToolParams) {
	t.Helper()
	result, err := session.CallTool(ctx, params)
	require.NoError(t, err)
	require.False(t, result.IsError, "the %s call must succeed", params.Name)
}

// handlerAttributionUnsearchableQuery is one token long enough that its
// edit-distance-1 automaton exceeds Bleve's Levenshtein DFA state limit, so
// the real index fails the search without a production seam.
func handlerAttributionUnsearchableQuery() string {
	return strings.Repeat("ab", handlerAttributionUnsearchLen/2)
}

func TestSessionAttribution_HandlerEntriesCarryTheReadyLineSession(t *testing.T) {
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"auth-guide.md":   handlerAttributionKeptDoc,
			"doomed-guide.md": handlerAttributionDoomedDoc,
		},
		Prompts: map[string]string{"greet.md": handlerAttributionPromptDoc},
	})
	args := testkit.SharedLaunchArgs(contentDir)
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()

	var l *testkit.LaunchUnderTest
	var session *mcp.ClientSession
	h := runWinningColdStart(t, func(t *testing.T, h *testkit.SharedServerHarness) {
		l = h.Launch(args...)
		session = connectAndListTools(t, l)
	})
	id := h.Identity(args)
	sessionID := proxyAttachedSession(t, l)

	// The resource handler is reachable only for a registered URI, so its
	// failure needs a URI the registry still holds when the request is routed
	// but the catalog no longer holds once the handler revalidates. No
	// handler has run yet, so this first request's revalidation is not
	// debounced and observes the deletion.
	resourcesDir := content.NewContentProvider(contentDir).ResourcesDir
	require.NoError(t, os.Remove(filepath.Join(resourcesDir, "doomed-guide.md")))
	_, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: handlerAttributionDoomedURI})
	require.Error(t, err)

	_, err = session.ReadResource(ctx, &mcp.ReadResourceParams{URI: handlerAttributionKeptURI})
	require.NoError(t, err)
	requireToolCallSucceeded(t, ctx, session, &mcp.CallToolParams{Name: "read", Arguments: map[string]any{"uri": handlerAttributionKeptURI}})
	requireToolCallFailed(t, ctx, session, &mcp.CallToolParams{Name: "read", Arguments: map[string]any{"uri": handlerAttributionMissingURI}})
	_, err = session.GetPrompt(ctx, &mcp.GetPromptParams{Name: handlerAttributionPrompt, Arguments: map[string]string{handlerAttributionPromptArg: "world"}})
	require.NoError(t, err)
	_, err = session.GetPrompt(ctx, &mcp.GetPromptParams{Name: handlerAttributionPrompt})
	require.Error(t, err)
	unsearchable := handlerAttributionUnsearchableQuery()
	requireToolCallFailed(t, ctx, session, &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": unsearchable}})
	logs := readSharedServerLog(t, h, id)

	expected := []handlerAttributionEntry{
		{name: "resource request (failing)", level: "INFO", msg: "Resource request", attrs: "uri=" + handlerAttributionDoomedURI, count: 1},
		{name: "resource read failure", level: "ERROR", msg: "Resource read failed", attrs: "uri=" + handlerAttributionDoomedURI, errored: true, count: 1},
		{name: "resource request", level: "INFO", msg: "Resource request", attrs: "uri=" + handlerAttributionKeptURI, count: 1},
		{name: "read request", level: "INFO", msg: "Get resource request", attrs: "uri=" + handlerAttributionKeptURI, count: 1},
		{name: "read request (failing)", level: "INFO", msg: "Get resource request", attrs: "uri=" + handlerAttributionMissingURI, count: 1},
		{name: "read failure", level: "ERROR", msg: "Get resource failed", attrs: "uri=" + handlerAttributionMissingURI, errored: true, count: 1},
		{name: "prompt requests", level: "INFO", msg: "Prompt request", attrs: "name=" + handlerAttributionPrompt, count: 2},
		{name: "prompt failure", level: "ERROR", msg: "Prompt retrieval failed", attrs: "name=" + handlerAttributionPrompt, errored: true, count: 1},
		{name: "search failure", level: "ERROR", msg: "Search failed", attrs: "query=" + unsearchable, errored: true, count: 1},
	}
	for _, entry := range expected {
		assert.Len(t, handlerAttributionLinesMatching(logs, entry.pattern(sessionID)), entry.count, "%s entry:\n%s", entry.name, logs)
	}
}

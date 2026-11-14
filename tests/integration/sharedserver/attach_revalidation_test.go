//go:build unix

package sharedserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	attachRevalidationSeedDoc = "---\nname: Authentication Guide\ndescription: Guide for authentication\n---\nThis document covers authentication.\n"
	attachRevalidationLateDoc = "---\nname: Late Guide\ndescription: Added while the shared server runs\n---\nThe unique term latecomer lives here.\n"
	attachRevalidationSeedURI = "acdc://auth-guide"
	attachRevalidationLateURI = "acdc://late-guide"
)

func newAttachRevalidationContentDir(t *testing.T) (contentDir, resourcesDir string) {
	t.Helper()
	contentDir = testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{"auth-guide.md": attachRevalidationSeedDoc},
	})
	return contentDir, content.NewContentProvider(contentDir).ResourcesDir
}

func launchAttachRevalidationServer(t *testing.T, contentDir string) (server *sharedServerUnderTest, logDir string) {
	t.Helper()
	server = newSharedServerUnderTest(t, newSharedServerSocketTestEndpointDir(t))
	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{Transport: "stdio"})
	require.NoError(t, flags.Set("shared-server-identity", server.identity))

	params := app.DefaultRunParams()
	params.EndpointDir = func() (string, error) { return server.endpointDir, nil }
	logDir = t.TempDir()
	params.LogDir = func() (string, error) { return logDir, nil }

	ctx, cancel := context.WithCancel(context.Background())
	server.cancel = cancel
	go func() { server.done <- app.RunWithDeps(ctx, params, flags, "test") }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-server.done:
			if err != nil {
				t.Logf("shared server stopped with: %v", err)
			}
		case <-time.After(sharedServerSocketTestDeadline):
			t.Errorf("shared server did not stop")
		}
	})
	return server, logDir
}

func attachRevalidationSession(t *testing.T, server *sharedServerUnderTest) *sharedServerLineClient {
	t.Helper()
	client := server.dial(t)
	client.readReadyLine(t)
	client.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "attach-revalidation-client", "version": "1.0"},
		},
	})
	initialized := client.responseTo(t, 1)
	require.Nil(t, initialized["error"], "initialize failed: %v", initialized)
	client.send(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return client
}

func attachRevalidationResourceURIs(t *testing.T, client *sharedServerLineClient, id float64) []string {
	t.Helper()
	client.send(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": "resources/list", "params": map[string]any{}})
	listed := client.responseTo(t, id)
	require.Nil(t, listed["error"], "resources/list failed: %v", listed)
	result, ok := listed["result"].(map[string]any)
	require.True(t, ok, "resources/list result: %v", listed)
	entries, ok := result["resources"].([]any)
	require.True(t, ok, "resources/list resources: %v", result)
	uris := make([]string, 0, len(entries))
	for _, entry := range entries {
		resource, ok := entry.(map[string]any)
		require.True(t, ok, "resource entry: %v", entry)
		uri, _ := resource["uri"].(string)
		uris = append(uris, uri)
	}
	return uris
}

func attachRevalidationSearch(t *testing.T, client *sharedServerLineClient, id float64, query string) {
	t.Helper()
	client.send(t, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": "search", "arguments": map[string]any{"query": query}},
	})
	searched := client.responseTo(t, id)
	require.Nil(t, searched["error"], "search failed: %v", searched)
}

func TestSharedServerAttach_NewSessionListsADocumentAddedWithinTheDebounceWindow(t *testing.T) {
	contentDir, resourcesDir := newAttachRevalidationContentDir(t)
	server, _ := launchAttachRevalidationServer(t, contentDir)

	first := attachRevalidationSession(t, server)
	// Starts a debounce window, so any refresh a request could trigger before
	// the next attach is suppressed: only the attach revalidation can see the
	// new document in time.
	attachRevalidationSearch(t, first, 2, "authentication")

	require.NoError(t, os.WriteFile(filepath.Join(resourcesDir, "late-guide.md"), []byte(attachRevalidationLateDoc), 0o644))

	second := attachRevalidationSession(t, server)
	uris := attachRevalidationResourceURIs(t, second, 2)

	assert.Contains(t, uris, attachRevalidationLateURI)
	assert.Contains(t, uris, attachRevalidationSeedURI)
}

func TestSharedServerAttach_FailedRevalidationIsLoggedKeepsTheCatalogAndSendsTheReadyLine(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the walk cannot be made to fail")
	}
	contentDir, resourcesDir := newAttachRevalidationContentDir(t)
	server, logDir := launchAttachRevalidationServer(t, contentDir)
	attachRevalidationSession(t, server)

	unreadable := filepath.Join(resourcesDir, "unreadable")
	require.NoError(t, os.Mkdir(unreadable, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(unreadable, "hidden.md"), []byte(attachRevalidationLateDoc), 0o644))
	require.NoError(t, os.Chmod(unreadable, 0o000))
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o755) })

	second := attachRevalidationSession(t, server)

	logged, err := os.ReadFile(filepath.Join(logDir, server.identity+".log"))
	require.NoError(t, err)
	assert.Contains(t, string(logged), "Catalog refresh failed")
	assert.Equal(t, []string{attachRevalidationSeedURI}, attachRevalidationResourceURIs(t, second, 2))
}

func TestSharedServerAttach_SearchBySessionANotifiesSessionBOfTheListChange(t *testing.T) {
	contentDir, resourcesDir := newAttachRevalidationContentDir(t)
	server, _ := launchAttachRevalidationServer(t, contentDir)
	sessionA := attachRevalidationSession(t, server)
	sessionB := attachRevalidationSession(t, server)

	require.NoError(t, os.WriteFile(filepath.Join(resourcesDir, "late-guide.md"), []byte(attachRevalidationLateDoc), 0o644))
	attachRevalidationSearch(t, sessionA, 2, "latecomer")

	for {
		msg := sessionB.readLine(t)
		if msg["method"] == "notifications/resources/list_changed" {
			break
		}
	}
	assert.Contains(t, attachRevalidationResourceURIs(t, sessionB, 2), attachRevalidationLateURI)
}

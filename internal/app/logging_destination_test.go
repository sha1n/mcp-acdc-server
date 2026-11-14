package app

import (
	"context"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const loggingDestinationTestDeadline = 10 * time.Second

// writeLoggingDestinationContent lays out a legacy content root, the layout
// whose discovery logs every loaded resource, with a single resource carrying
// the given name so a test can tell apart the discovery entries of two builds.
func writeLoggingDestinationContent(t *testing.T, resourceName string) string {
	t.Helper()
	contentDir := t.TempDir()
	cp := content.NewContentProvider(contentDir)
	require.NoError(t, os.MkdirAll(cp.ResourcesDir, 0o755))
	metadata := "server:\n  name: logging\n  version: 1.0\n  instructions: inst\ntools: []\n"
	require.NoError(t, os.WriteFile(cp.ConfigFile, []byte(metadata), 0o644))
	doc := "---\nname: " + resourceName + "\ndescription: Logging destination fixture\n---\n# Guide\n\nBody.\n"
	require.NoError(t, os.WriteFile(filepath.Join(cp.ResourcesDir, "guide.md"), []byte(doc), 0o644))
	return contentDir
}

func loggingDestinationFlags(t *testing.T, contentDir string) *pflag.FlagSet {
	t.Helper()
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("content-dir", contentDir))
	require.NoError(t, flags.Set("transport", "stdio"))
	return flags
}

// captureOSStderr must run before DefaultRunParams so that whatever the
// params capture from os.Stderr is the capture file.
func captureOSStderr(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stderr-")
	require.NoError(t, err)
	original := os.Stderr
	os.Stderr = file
	t.Cleanup(func() {
		os.Stderr = original
		_ = file.Close()
	})
	return file
}

func readCapturedFile(t *testing.T, file *os.File) string {
	t.Helper()
	content, err := os.ReadFile(file.Name())
	require.NoError(t, err)
	return string(content)
}

// runOrdinaryStdioSession runs RunWithDeps over an in-memory transport, waits
// for a client to complete the MCP handshake, then disconnects the client so
// the run ends on its own.
func runOrdinaryStdioSession(t *testing.T, params RunParams, flags *pflag.FlagSet) {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	params.CustomIOTransport = serverTransport
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- RunWithDeps(ctx, params, flags, identity.Binary{Version: "9.8.7"}) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "logging-destination-client", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	require.NoError(t, session.Close())

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(loggingDestinationTestDeadline):
		t.Fatal("the ordinary run did not end after its client disconnected")
	}
}

func loadedResourceEntry(resourceName string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^time=\S+ level=INFO msg="Loaded resource" uri=\S+ name="` + regexp.QuoteMeta(resourceName) + `"$`)
}

func TestRunWithDeps_OrdinaryStdioLaunchWritesTextEntriesToStderr(t *testing.T) {
	stderr := captureOSStderr(t)
	params := DefaultRunParams()
	contentDir := writeLoggingDestinationContent(t, "Stderr Guide")

	runOrdinaryStdioSession(t, params, loggingDestinationFlags(t, contentDir))

	logged := readCapturedFile(t, stderr)
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="Starting MCP Acdc server" version=9\.8\.7$`, logged)
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="Config: content_dir" value=`+regexp.QuoteMeta(contentDir)+`$`, logged)
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="Config: transport" value=stdio$`, logged)
	assert.Regexp(t, loadedResourceEntry("Stderr Guide"), logged)
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="Registered tool" name=search$`, logged)
}

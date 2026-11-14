//go:build unix

package app

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func awaitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(loggingDestinationTestDeadline):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestRunWithDeps_OrdinaryLaunchDoesNotRedirectAConcurrentSharedServersEntries(t *testing.T) {
	sharedStderr := &lockedBuffer{}
	sharedParams := DefaultRunParams()
	sharedParams.Stderr = sharedStderr
	buildEntered := make(chan struct{})
	releaseBuild := make(chan struct{})
	var release sync.Once
	sharedParams.CreateServer = func(ctx context.Context, logger *slog.Logger, settings *config.Settings, version string) (Components, error) {
		close(buildEntered)
		<-releaseBuild
		return CreateServer(ctx, logger, settings, version)
	}
	shared := startLoggingSharedServer(t, sharedParams,
		sharedServerLoggingFlags(t, writeLoggingDestinationContent(t, "Shared Guide")))
	// Registered after the shared server's own cleanup so it runs first and a
	// failing test cannot leave the build blocked while the server stops.
	t.Cleanup(func() { release.Do(func() { close(releaseBuild) }) })
	awaitClosed(t, buildEntered, "the shared server's build to start")

	runnerStderr := &lockedBuffer{}
	runnerParams := DefaultRunParams()
	runnerParams.Stderr = runnerStderr
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	runnerParams.CustomIOTransport = serverTransport
	runnerCtx, cancelRunner := context.WithCancel(context.Background())
	t.Cleanup(cancelRunner)
	runnerDone := make(chan error, 1)
	runnerFlags := loggingDestinationFlags(t, writeLoggingDestinationContent(t, "Runner Guide"))
	go func() { runnerDone <- RunWithDeps(runnerCtx, runnerParams, runnerFlags, "9.8.7") }()
	client := mcp.NewClient(&mcp.Implementation{Name: "logging-destination-client", Version: "1.0"}, nil)
	session, err := client.Connect(runnerCtx, clientTransport, nil)
	require.NoError(t, err)

	release.Do(func() { close(releaseBuild) })
	shared.awaitReady(t)
	require.NoError(t, session.Close())
	select {
	case err := <-runnerDone:
		require.NoError(t, err)
	case <-time.After(loggingDestinationTestDeadline):
		t.Fatal("the ordinary run did not end after its client disconnected")
	}
	shared.stop(t)

	sharedLog := shared.logged(t)
	runnerLog := runnerStderr.String()
	assert.Regexp(t, loadedResourceEntry("Shared Guide"), sharedLog)
	assert.NotContains(t, runnerLog, "Shared Guide")
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="Starting MCP Acdc server" version=9\.8\.7$`, runnerLog)
	assert.NotContains(t, sharedLog, `msg="Starting MCP Acdc server"`)
	assert.Regexp(t, loadedResourceEntry("Runner Guide"), runnerLog)
	assert.NotContains(t, sharedLog, "Runner Guide")
	assert.Empty(t, sharedStderr.String(), "the shared server path must never use Stderr")
}

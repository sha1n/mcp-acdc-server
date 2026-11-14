//go:build unix

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var selfCheckTestBinary = identity.Binary{Version: "1.0.0-test", Build: "test"}

func selfCheckTestSettings(t *testing.T) *config.Settings {
	t.Helper()
	contentDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(contentDir, ".acdc"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(contentDir, ".acdc", "config.yaml"), []byte("server:\n  name: test\n"), 0o600))
	return &config.Settings{ContentDir: contentDir, SharedServer: config.SharedServerSettings{Linger: time.Hour}}
}

func selfCheckTestParams(t *testing.T, created *bool) (RunParams, string) {
	t.Helper()
	endpointDir, err := os.MkdirTemp("/tmp", "acdc-chk-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(endpointDir) })
	logDir := t.TempDir()
	return RunParams{
		CreateServer: sharedServerCreateSpy(created),
		EndpointDir:  func() (string, error) { return endpointDir, nil },
		LogDir:       func() (string, error) { return logDir, nil },
	}, endpointDir
}

func TestRunSharedServer_MismatchedIdentityExitsBeforeBind(t *testing.T) {
	settings := selfCheckTestSettings(t)
	created := false
	params, endpointDir := selfCheckTestParams(t, &created)

	err := runSharedServer(context.Background(), params, settings, sharedServerFlagTestIdentity, selfCheckTestBinary)

	require.ErrorIs(t, err, sharedserver.ErrIdentityMismatch)
	assert.False(t, created)
	assert.NoFileExists(t, filepath.Join(endpointDir, sharedServerFlagTestIdentity+".sock"))
}

func TestRunSharedServer_ChecksTheIdentityOfTheLaunchSettingsAndBinary(t *testing.T) {
	settings := selfCheckTestSettings(t)
	id, err := identity.Compute(settings, selfCheckTestBinary)
	require.NoError(t, err)
	created := false
	params, endpointDir := selfCheckTestParams(t, &created)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runSharedServer(ctx, params, settings, id, selfCheckTestBinary) }()

	awaitSharedServerSocket(t, filepath.Join(endpointDir, id+".sock"))
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the shared server did not stop after cancellation")
	}
	assert.True(t, created, "a shared server whose identity matches its content must build")
}

func TestRunSharedServer_OtherBinaryIsAMismatch(t *testing.T) {
	settings := selfCheckTestSettings(t)
	id, err := identity.Compute(settings, identity.Binary{Version: "2.0.0-other", Build: "other"})
	require.NoError(t, err)
	created := false
	params, _ := selfCheckTestParams(t, &created)

	err = runSharedServer(context.Background(), params, settings, id, selfCheckTestBinary)

	require.ErrorIs(t, err, sharedserver.ErrIdentityMismatch, "the self-check must use the binary metadata of the launch")
	assert.False(t, created)
}

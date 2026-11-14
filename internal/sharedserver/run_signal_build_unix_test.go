//go:build unix

package sharedserver

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_CancellingTheRunContextDuringTheBuildIsANormalStop(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, testIdentity+".sock")
	build, observed := buildGatedOnItsContext(socketPath, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Identity: testIdentity, EndpointDir: dir, Build: build, Linger: time.Hour})
	}()
	_, _ = dialAcceptorTestSocket(t, socketPath)

	cancel()

	require.NoError(t, awaitRunResult(t, done), "a signal during the build is a normal stop, as after it")
	observation := <-observed
	assert.True(t, observation.cancelled, "the build must see the cancellation")
	info, err := os.Lstat(socketPath)
	require.NoError(t, err, "a signal stop keeps the socket path, as after the build")
	assert.Equal(t, fs.ModeSocket, info.Mode().Type())
	assertLockReleased(t, dir)
}

//go:build unix

package app

import (
	"context"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunSharedServer_SIGTERMAndSIGINTEndItImmediately(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			// Keeps the test binary alive if the shared server does not handle
			// the signal itself.
			guard := make(chan os.Signal, 1)
			signal.Notify(guard, sig)
			t.Cleanup(func() { signal.Stop(guard) })

			endpointDir, err := os.MkdirTemp("/tmp", "acdc-sig-")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.RemoveAll(endpointDir) })
			created := false
			params := RunParams{
				CreateServer: sharedServerCreateSpy(&created),
				EndpointDir:  func() (string, error) { return endpointDir, nil },
			}
			settings := &config.Settings{SharedServer: config.SharedServerSettings{Linger: time.Hour}}

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			done := make(chan error, 1)
			go func() {
				done <- runSharedServer(ctx, params, settings, sharedServerFlagTestIdentity, "test")
			}()
			awaitSharedServerSocket(t, filepath.Join(endpointDir, sharedServerFlagTestIdentity+".sock"))

			require.NoError(t, syscall.Kill(os.Getpid(), sig))

			select {
			case err := <-done:
				assert.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatalf("the shared server did not exit on %s", sig)
			}
		})
	}
}

func awaitSharedServerSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if info, err := os.Lstat(socketPath); err == nil && info.Mode().Type() == fs.ModeSocket {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket %s was never bound", socketPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

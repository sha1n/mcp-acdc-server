//go:build unix

package sharedserver

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_LogDirectoryEqualToTheEndpointDirectoryIsUnusable(t *testing.T) {
	tests := []struct {
		name   string
		logDir func(endpointDir string) string
	}{
		{name: "same path", logDir: func(dir string) string { return dir }},
		{name: "same path after cleaning", logDir: func(dir string) string { return dir + "/./" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := shortTempDir(t)
			build, _ := buildSpy(nil)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- Run(ctx, Config{
					Identity:    testIdentity,
					EndpointDir: dir,
					LogDir:      tt.logDir(dir),
					LogStartup:  func(logger *slog.Logger) { logger.Info("startup entry") },
					Build:       build,
				})
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					assert.NoError(t, err)
				case <-time.After(serveTestDeadline):
					t.Error("Run did not stop")
				}
			})

			conn := dialAttachRevalidateSocket(t, filepath.Join(dir, testIdentity+".sock"), done)
			require.NoError(t, conn.SetReadDeadline(time.Now().Add(serveTestDeadline)))
			line, err := bufio.NewReader(conn).ReadBytes('\n')
			require.NoError(t, err)
			ready, err := DecodeReadyLine(line)
			require.NoError(t, err)

			assert.Empty(t, ready.Log, "the log must never live in the endpoint directory")
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			var names []string
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			assert.ElementsMatch(t, []string{testIdentity + ".lock", testIdentity + ".sock"}, names)
		})
	}
}

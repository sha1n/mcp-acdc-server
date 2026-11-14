//go:build unix

package app

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunSharedServer_RejectsANonAbsoluteInjectedEndpointDirectory(t *testing.T) {
	tests := []struct {
		name string
		dir  string
	}{
		{name: "abstract socket prefix", dir: "@runtime/acdc-mcp"},
		{name: "relative", dir: "runtime/acdc-mcp"},
		{name: "empty", dir: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd := t.TempDir()
			t.Chdir(cwd)
			created := false
			params := RunParams{
				CreateServer: sharedServerCreateSpy(&created),
				EndpointDir:  func() (string, error) { return tt.dir, nil },
			}

			// Cancelled so that, should the directory be accepted, Run stops
			// serving at once instead of blocking the test.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			err := runSharedServer(ctx, params, &config.Settings{}, sharedServerFlagTestIdentity, "test")

			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("endpoint directory %q is not an absolute path", tt.dir))
			assert.False(t, created, "no server may be built for a non-absolute endpoint directory")
			entries, err := os.ReadDir(cwd)
			require.NoError(t, err)
			assert.Empty(t, entries, "nothing may be created, bound or locked for a non-absolute endpoint directory")
		})
	}
}

//go:build unix

package testkit

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHoldSharedServer_HoldsASessionWithADecodedReadyLine(t *testing.T) {
	h := NewSharedServerHarness(t)
	args := SharedLaunchArgs(CreateTestContentDir(t, nil))

	id, held := h.HoldSharedServer(args)

	assert.Equal(t, h.Identity(args), id)
	assert.True(t, held.Ready.Ready)
	assert.NotEmpty(t, held.Ready.Session)
	assert.Equal(t, 1, h.Spawns())
	assert.Equal(t, 1, h.Builds())
	assert.Empty(t, h.SharedServerExits())
}

func TestHoldSharedServer_FailsWhenTheServerEndsBeforeASessionHoldsIt(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, h *SharedServerHarness, id string)
		want    string
	}{
		{
			name: "crash with an error",
			prepare: func(t *testing.T, h *SharedServerHarness, id string) {
				// A non-empty directory at the socket path makes the bind fail.
				socketPath, err := h.Endpoint().SocketPath(id)
				require.NoError(t, err)
				require.NoError(t, os.MkdirAll(socketPath, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(socketPath, "occupied"), nil, 0o600))
			},
			want: "stale socket",
		},
		{
			name: "premature clean exit",
			prepare: func(t *testing.T, h *SharedServerHarness, id string) {
				// A candidate that finds the lock taken exits without an error.
				h.HoldLock(id)
			},
			want: "Err:<nil>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewSharedServerHarness(t)
			args := SharedLaunchArgs(CreateTestContentDir(t, nil))
			require.NoError(t, os.MkdirAll(h.EndpointDir, 0o700))
			tt.prepare(t, h, h.Identity(args))

			_, held, err := h.holdSharedServer(args, time.Now().Add(SharedServerDeadline))

			require.Error(t, err)
			assert.Nil(t, held)
			assert.Contains(t, err.Error(), "ended before a session held it")
			assert.Contains(t, err.Error(), tt.want)
			assert.Equal(t, 1, h.Spawns())
		})
	}
}

func TestHoldSharedServer_LingersBeyondItsDeadlineWithoutChangingTheIdentity(t *testing.T) {
	h := NewSharedServerHarness(t)
	args := SharedLaunchArgs(CreateTestContentDir(t, nil))

	assert.Greater(t, heldLinger, SharedServerDeadline)
	assert.Equal(t, h.Identity(args), h.Identity(append(args, "--shared-server-linger", heldLinger.String())))
}

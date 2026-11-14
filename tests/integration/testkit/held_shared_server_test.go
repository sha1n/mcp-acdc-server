//go:build unix

package testkit

import (
	"context"
	"errors"
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

func TestHoldSharedServer_IgnoresTheExitOfAnotherCandidate(t *testing.T) {
	h := NewSharedServerHarness(t)
	args := SharedLaunchArgs(CreateTestContentDir(t, nil))
	release := gateBuilds(h)
	result := holdInBackground(h, args, time.Now().Add(SharedServerDeadline))
	h.Eventually(func() bool { return h.Builds() == 1 }, "the held candidate does not start its build")

	// A candidate that finds its lock taken exits before any build, so the
	// gate holds only the held candidate.
	otherArgs := SharedLaunchArgs(CreateTestContentDir(t, nil))
	otherID := h.Identity(otherArgs)
	h.HoldLock(otherID)
	require.NoError(t, h.SpawnSharedServer(otherArgs)(otherID))
	h.Eventually(func() bool { return len(h.SharedServerExits()) == 1 }, "the other candidate's exit is not recorded")
	release <- nil

	got := awaitHoldResult(t, result)
	require.NoError(t, got.err)
	require.NotNil(t, got.held)
	assert.True(t, got.held.Ready.Ready)
	assert.NotEmpty(t, got.held.Ready.Session)
	assert.Equal(t, 1, h.Builds())
	assert.Equal(t, []SharedServerExit{{Built: false, Err: nil}}, h.SharedServerExits())
}

func TestHoldSharedServer_ReportsAnExitWhileItsAttachWaitsWithoutAwaitingTheDeadline(t *testing.T) {
	h := NewSharedServerHarness(t)
	args := SharedLaunchArgs(CreateTestContentDir(t, nil))
	release := gateBuilds(h)
	// The helper's deadline outlasts the watchdog of awaitHoldResult, so only
	// the candidate's exit can end its attach before the watchdog fires.
	result := holdInBackground(h, args, time.Now().Add(2*SharedServerDeadline))
	h.Eventually(func() bool { return h.Builds() == 1 }, "the held candidate does not start its build")

	release <- errBuildWithheld

	got := awaitHoldResult(t, result)
	require.Error(t, got.err)
	assert.Nil(t, got.held)
	assert.Contains(t, got.err.Error(), "ended before a session held it")
	assert.Contains(t, got.err.Error(), errBuildWithheld.Error())
	assert.Equal(t, 1, h.Spawns())
}

var errBuildWithheld = errors.New("build withheld by the test")

// gateBuilds holds every build of h until the test sends the build's result
// on the returned channel; a nil result lets the build proceed. It must be
// called before the first spawn.
func gateBuilds(h *SharedServerHarness) chan<- error {
	release := make(chan error, 1)
	h.buildGate = func(ctx context.Context) error {
		select {
		case err := <-release:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return release
}

type holdResult struct {
	held *RawSession
	err  error
}

func holdInBackground(h *SharedServerHarness, args []string, deadline time.Time) <-chan holdResult {
	result := make(chan holdResult, 1)
	go func() {
		_, held, err := h.holdSharedServer(args, deadline)
		result <- holdResult{held: held, err: err}
	}()
	return result
}

func awaitHoldResult(t *testing.T, result <-chan holdResult) holdResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(SharedServerDeadline):
		t.Fatalf("the helper did not return within %s", SharedServerDeadline)
		return holdResult{}
	}
}

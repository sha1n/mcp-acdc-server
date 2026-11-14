//go:build unix

package sharedserver

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionAcceptor_AConnectionCancelledByTheTimeTheBuildCompletesGetsNoReadyLine(t *testing.T) {
	acceptorListener, acceptorSocket := bindTestEndpoint(t)
	ep, err := endpoint.OpenEndpoint(filepath.Dir(acceptorSocket))
	require.NoError(t, err)
	peerListener, peerSocket := bindTestEndpoint(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	acceptor := startAccepting(ctx, acceptorListener, ep, testIdentity, time.Hour)
	cancel()
	// Both the build and the cancellation are now observable to every
	// session, as when a build completes just after a signal.
	require.NoError(t, acceptor.serve(server, nil, ""))

	// Each round is a fresh chance for the select to pick the build.
	for range 50 {
		client, err := net.Dial("unix", peerSocket)
		require.NoError(t, err)
		require.NoError(t, client.SetDeadline(time.Now().Add(serveTestDeadline)))
		conn, err := peerListener.AcceptUnix()
		require.NoError(t, err)

		acceptor.linger.attach()
		acceptor.handle(conn)

		received, err := io.ReadAll(client)
		_ = client.Close()
		require.NoError(t, err)
		assert.Empty(t, received, "a cancelled session must be closed without a ready line")
	}
}

func TestSessionAcceptor_AnAcceptFailureDuringTheBuildCancelsTheBuildAndIsReturned(t *testing.T) {
	tests := []struct {
		name   string
		finish func(acceptor *sessionAcceptor, buildCtx context.Context) error
	}{
		{
			name: "build fails on the cancellation",
			finish: func(acceptor *sessionAcceptor, buildCtx context.Context) error {
				return acceptor.abandon(buildCtx.Err())
			},
		},
		{
			name: "build completes after the cancellation",
			finish: func(acceptor *sessionAcceptor, _ context.Context) error {
				return acceptor.serve(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), nil, "")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listener, socketPath := bindTestEndpoint(t)
			ep, err := endpoint.OpenEndpoint(filepath.Dir(socketPath))
			require.NoError(t, err)
			acceptor := startAccepting(context.Background(), listener, ep, testIdentity, time.Hour)
			buildCtx := acceptor.buildContext()

			// An expired deadline makes AcceptUnix fail with a non-context error
			// while the listener stays open, standing in for EMFILE and similar.
			require.NoError(t, listener.SetDeadline(time.Now()))

			select {
			case <-buildCtx.Done():
			case <-time.After(serveTestDeadline):
				t.Fatal("an accept failure during the build must cancel the build")
			}
			err = tt.finish(acceptor, buildCtx)
			require.ErrorIs(t, err, os.ErrDeadlineExceeded, "the accept failure is what Run reports")
			assert.Contains(t, err.Error(), "failed to accept shared server connection")
		})
	}
}

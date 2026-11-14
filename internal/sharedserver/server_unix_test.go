//go:build unix

package sharedserver

import (
	"bufio"
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

const serveTestDeadline = 10 * time.Second

func TestServe_AcceptFailureClosesListenerAndIdleSessions(t *testing.T) {
	listener, socketPath := bindTestEndpoint(t)

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	ep, epErr := endpoint.OpenEndpoint(filepath.Dir(socketPath))
	require.NoError(t, epErr)
	done := make(chan error, 1)
	go func() {
		done <- startAccepting(context.Background(), listener, ep, testIdentity, time.Hour).serve(server, nil, "")
	}()

	client, err := net.Dial("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.SetDeadline(time.Now().Add(serveTestDeadline)))
	reader := bufio.NewReader(client)
	_, err = reader.ReadBytes('\n')
	require.NoError(t, err, "the idle session must have been accepted and attached")

	// An expired deadline makes AcceptUnix fail with a non-context error while
	// the listener stays open, standing in for EMFILE and similar failures.
	require.NoError(t, listener.SetDeadline(time.Now()))

	select {
	case err := <-done:
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrDeadlineExceeded)
	case <-time.After(serveTestDeadline):
		t.Fatal("serve did not return after the accept failure while a session was idle")
	}

	_, err = reader.ReadByte()
	assert.ErrorIs(t, err, io.EOF, "the idle session must be closed")

	probe, err := net.DialTimeout("unix", socketPath, time.Second)
	if err == nil {
		_ = probe.Close()
	}
	assert.Error(t, err, "the listener must be closed")
}

func TestServe_ContextCancellationReturnsNil(t *testing.T) {
	listener, socketPath := bindTestEndpoint(t)

	ep, epErr := endpoint.OpenEndpoint(filepath.Dir(socketPath))
	require.NoError(t, epErr)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- startAccepting(ctx, listener, ep, testIdentity, time.Hour).serve(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), nil, "")
	}()
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(serveTestDeadline):
		t.Fatal("serve did not return after cancellation")
	}
}

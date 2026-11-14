//go:build unix

package sharedserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type gatedBuildObservation struct {
	cancelled  bool
	socketGone bool
}

// buildGatedOnItsContext stands in for a build that outlasts the linger: it
// returns only once its context is cancelled, or fails after the deadline.
func buildGatedOnItsContext(socketPath string, server *mcp.Server, cleanup func()) (BuildFunc, <-chan gatedBuildObservation) {
	observed := make(chan gatedBuildObservation, 1)
	build := func(ctx context.Context) (*mcp.Server, func(), error) {
		select {
		case <-ctx.Done():
			_, err := os.Lstat(socketPath)
			observed <- gatedBuildObservation{cancelled: true, socketGone: errors.Is(err, fs.ErrNotExist)}
			if server != nil {
				return server, cleanup, nil
			}
			return nil, nil, ctx.Err()
		case <-time.After(serveTestDeadline):
			observed <- gatedBuildObservation{}
			return nil, nil, errors.New("the build context was never cancelled")
		}
	}
	return build, observed
}

func awaitRunResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * serveTestDeadline):
		t.Fatal("Run did not return")
		return nil
	}
}

func TestRun_LingerExpiryDuringTheBuildCancelsTheBuildAndReturnsNil(t *testing.T) {
	tests := []struct {
		name     string
		succeeds bool
	}{
		{name: "build fails on the cancellation", succeeds: false},
		{name: "build completes after the cancellation", succeeds: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := shortTempDir(t)
			socketPath := filepath.Join(dir, testIdentity+".sock")
			var server *mcp.Server
			cleanedUp := false
			if tt.succeeds {
				server = mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
			}
			build, observed := buildGatedOnItsContext(socketPath, server, func() { cleanedUp = true })
			startedAt := time.Now()
			done := make(chan error, 1)
			go func() {
				done <- Run(context.Background(), Config{Identity: testIdentity, EndpointDir: dir, Build: build, Linger: time.Second})
			}()

			err := awaitRunResult(t, done)

			require.NoError(t, err, "a linger exit during the build is a normal exit")
			observation := <-observed
			assert.True(t, observation.cancelled, "linger expiry must cancel the build's context")
			assert.True(t, observation.socketGone, "the socket must be unlinked before the build is cancelled")
			assert.GreaterOrEqual(t, time.Since(startedAt), time.Second)
			assert.Equal(t, tt.succeeds, cleanedUp, "a completed build must be cleaned up as on any exit")
			assertLockReleased(t, dir)
		})
	}
}

func TestRun_CancellationRacingTheBuildCompletionDropsWaitingConnections(t *testing.T) {
	for round := range 10 {
		t.Run(fmt.Sprintf("round %d", round), func(t *testing.T) {
			dir := shortTempDir(t)
			socketPath := filepath.Join(dir, testIdentity+".sock")
			release := make(chan struct{})
			build := func(context.Context) (*mcp.Server, func(), error) {
				<-release
				return mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), nil, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- Run(ctx, Config{Identity: testIdentity, EndpointDir: dir, Build: build, Linger: time.Hour})
			}()
			var conns []io.Reader
			for range 4 {
				_, reader := dialAcceptorTestSocket(t, socketPath)
				conns = append(conns, reader)
			}

			var racing sync.WaitGroup
			racing.Go(cancel)
			racing.Go(func() { close(release) })
			racing.Wait()

			require.NoError(t, awaitRunResult(t, done))
			for _, conn := range conns {
				_, _ = io.Copy(io.Discard, conn)
			}
		})
	}
}

type gatedToolClient struct {
	t      *testing.T
	writer io.Writer
	lines  *json.Decoder
}

func (c *gatedToolClient) send(msg map[string]any) {
	c.t.Helper()
	b, err := json.Marshal(msg)
	require.NoError(c.t, err)
	_, err = fmt.Fprintf(c.writer, "%s\n", b)
	require.NoError(c.t, err)
}

func (c *gatedToolClient) next() (map[string]any, error) {
	var msg map[string]any
	err := c.lines.Decode(&msg)
	return msg, err
}

func TestRun_CancellationDropsTheConnectionBeforeAContextIgnoringHandlerReturns(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, testIdentity+".sock")
	gate := make(chan struct{})
	var releaseGate sync.Once
	t.Cleanup(func() { releaseGate.Do(func() { close(gate) }) })
	started := make(chan struct{})
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "gated", Description: "ignores its context until released"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
			close(started)
			<-gate
			return &mcp.CallToolResult{}, struct{}{}, nil
		})
	build := func(context.Context) (*mcp.Server, func(), error) { return server, nil, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Identity: testIdentity, EndpointDir: dir, Build: build, Linger: time.Hour})
	}()

	conn, reader := dialAcceptorTestSocket(t, socketPath)
	client := &gatedToolClient{t: t, writer: conn, lines: json.NewDecoder(reader)}
	ready, err := client.next()
	require.NoError(t, err)
	require.Equal(t, true, ready["ready"])
	client.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "gated-tool-client", "version": "1.0"},
		},
	})
	initialized, err := client.next()
	require.NoError(t, err)
	require.Equal(t, float64(1), initialized["id"])
	client.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	client.send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "gated", "arguments": map[string]any{}},
	})
	select {
	case <-started:
	case <-time.After(serveTestDeadline):
		t.Fatal("the gated request never started")
	}

	cancelledAt := time.Now()
	cancel()

	for {
		msg, err := client.next()
		if err != nil {
			assert.ErrorIs(t, err, io.EOF, "the connection must simply drop")
			break
		}
		id, isResponse := msg["id"].(float64)
		assert.False(t, isResponse && id == 2, "the in-flight request must get no response: %v", msg)
	}
	assert.Less(t, time.Since(cancelledAt), serveTestDeadline, "the drop must not wait for the handler or the linger")

	releaseGate.Do(func() { close(gate) })
	assert.NoError(t, awaitRunResult(t, done))
}

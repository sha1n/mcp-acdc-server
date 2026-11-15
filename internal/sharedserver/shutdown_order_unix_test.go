//go:build unix

package sharedserver

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shutdownOrderRounds repeats the shutdown because, without an ordering, the
// listener close and the end of the sessions race and either may win a round.
const shutdownOrderRounds = 20

func TestRun_ASessionEndedByShutdownSeesARefusedDial(t *testing.T) {
	// One processor makes the goroutines woken by the cancellation run in
	// whatever order it readied them, instead of the listener close almost
	// always winning; an ordered shutdown passes either way.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	deadline := time.Now().Add(serveTestDeadline)
	for round := range shutdownOrderRounds {
		requireSessionEndPrecededByStopAccepting(t, deadline, round)
	}
}

func requireSessionEndPrecededByStopAccepting(t *testing.T, deadline time.Time, round int) {
	t.Helper()
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, testIdentity+".sock")
	bound := make(chan struct{})
	observing := make(chan struct{})
	dialAtEnd := make(chan error, 1)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	// The request context is the earliest point at which a session observes
	// its end, so the dial here is the first a recovering proxy could make.
	mcp.AddTool(server, &mcp.Tool{Name: "observe_end"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
			close(observing)
			<-ctx.Done()
			replacement, err := net.Dial("unix", socketPath)
			if err == nil {
				_ = replacement.Close()
			}
			dialAtEnd <- err
			return &mcp.CallToolResult{}, struct{}{}, nil
		})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Identity:        testIdentity,
			CurrentIdentity: currentIsTestIdentity,
			EndpointDir:     dir,
			LogDir:          filepath.Join(dir, "logs"),
			Linger:          time.Hour,
			// Build runs only once the socket is bound and accepting.
			Build: func(context.Context, *slog.Logger) (*mcp.Server, func(), error) {
				close(bound)
				return server, nil, nil
			},
		})
	}()

	select {
	case <-bound:
	case err := <-done:
		t.Fatalf("round %d: Run returned before binding: %v", round, err)
	case <-time.After(time.Until(deadline)):
		t.Fatalf("round %d: Run never bound", round)
	}
	session, err := net.Dial("unix", socketPath)
	require.NoError(t, err, "round %d", round)
	defer func() { _ = session.Close() }()
	require.NoError(t, session.SetDeadline(deadline))
	reader := bufio.NewReader(session)
	readyLine, err := reader.ReadString('\n')
	require.NoError(t, err, "round %d", round)
	require.Contains(t, readyLine, `"ready":true`, "round %d", round)

	writeAttachTestLine(t, session, attachTestInitialize)
	_, err = reader.ReadString('\n')
	require.NoError(t, err, "round %d", round)
	writeAttachTestLine(t, session, attachTestInitialized)
	writeAttachTestLine(t, session, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"observe_end","arguments":{}}}`)
	select {
	case <-observing:
	case <-time.After(time.Until(deadline)):
		t.Fatalf("round %d: the request never started", round)
	}

	cancel()
	select {
	case err := <-dialAtEnd:
		assert.ErrorIs(t, err, syscall.ECONNREFUSED, "round %d: once a session observes its end, the shared server must no longer accept", round)
	case <-time.After(time.Until(deadline)):
		t.Fatalf("round %d: the session never observed its end", round)
	}
	_, err = io.Copy(io.Discard, reader)
	require.NoError(t, err, "round %d: the session must end by EOF", round)

	select {
	case err := <-done:
		require.NoError(t, err, "round %d", round)
	case <-time.After(time.Until(deadline)):
		t.Fatalf("round %d: Run did not stop", round)
	}
}

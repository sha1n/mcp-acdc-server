package testkit

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestACDCService_StopReleasesTheServerWhenGracefulShutdownTimesOut(t *testing.T) {
	flags := NewTestFlags(t, CreateTestContentDir(t, nil), nil)
	service := NewACDCService("stop-shutdown-timeout", flags)
	acdc := service.(*acdcService)
	acdc.StopDelay = 200 * time.Millisecond

	var serverCtx context.Context
	runnerReturned := make(chan struct{})
	acdc.runner = func(ctx context.Context, params app.RunParams, flags *pflag.FlagSet, version string) error {
		defer close(runnerReturned)
		serverCtx = ctx
		return app.RunWithDeps(ctx, params, flags, version)
	}

	props, err := service.Start()
	require.NoError(t, err)

	// An event stream stays in flight for as long as its client holds it, so the
	// graceful shutdown cannot complete while this connection is open.
	stream, err := net.Dial("tcp", strings.TrimPrefix(props["acdc.baseURL"].(string), "http://"))
	require.NoError(t, err)
	defer func() { _ = stream.Close() }()
	_, err = stream.Write([]byte("GET /sse HTTP/1.1\r\nHost: localhost\r\n\r\n"))
	require.NoError(t, err)
	_, err = stream.Read(make([]byte, 1))
	require.NoError(t, err, "the server must be serving the stream before it is stopped")

	stopErr := service.Stop()

	require.ErrorIs(t, stopErr, context.DeadlineExceeded)

	select {
	case <-runnerReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("the server run did not return after Stop")
	}
	require.Error(t, serverCtx.Err(), "Stop must cancel the server context even when the graceful shutdown fails")

	require.NoError(t, stream.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, readErr := readUntilError(stream)
	require.False(t, errors.Is(readErr, os.ErrDeadlineExceeded),
		"Stop must close the connections the graceful shutdown left open, got %v", readErr)
}

func readUntilError(conn net.Conn) (int, error) {
	buf := make([]byte, 4096)
	total := 0
	for {
		n, err := conn.Read(buf)
		total += n
		if err != nil {
			return total, err
		}
	}
}

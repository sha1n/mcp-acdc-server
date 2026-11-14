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

func TestACDCService_StopAfterAFailedStartReturnsTheStartupErrorPromptly(t *testing.T) {
	startupErr := errors.New("startup failed")
	flags := NewTestFlags(t, CreateTestContentDir(t, nil), nil)
	service := NewACDCService("stop-after-failed-start", flags)
	acdc := service.(*acdcService)
	acdc.StopDelay = time.Minute
	acdc.runner = func(context.Context, app.RunParams, *pflag.FlagSet, string) error {
		return startupErr
	}

	_, err := service.Start()
	require.ErrorIs(t, err, startupErr)

	for attempt := 1; attempt <= 2; attempt++ {
		require.ErrorIs(t, stopWithin(t, service, 2*time.Second), startupErr, "Stop #%d", attempt)
	}
}

func TestACDCService_StopIsIdempotentAfterASuccessfulStart(t *testing.T) {
	flags := NewTestFlags(t, CreateTestContentDir(t, nil), nil)
	service := NewACDCService("double-stop", flags)
	service.(*acdcService).StopDelay = time.Minute

	_, err := service.Start()
	require.NoError(t, err)

	for attempt := 1; attempt <= 2; attempt++ {
		require.NoError(t, stopWithin(t, service, 5*time.Second), "Stop #%d", attempt)
	}
}

func TestACDCService_ConcurrentStopsShareTheRunnerResult(t *testing.T) {
	runnerErr := errors.New("runner failed")
	flags := NewTestFlags(t, CreateTestContentDir(t, nil), &FlagOptions{Transport: "stdio"})
	service := NewACDCService("concurrent-stop", flags)
	acdc := service.(*acdcService)
	acdc.StopDelay = time.Minute
	release := make(chan struct{})
	acdc.runner = func(context.Context, app.RunParams, *pflag.FlagSet, string) error {
		<-release
		return runnerErr
	}

	_, err := service.Start()
	require.NoError(t, err)

	const stoppers = 2
	entered := make(chan struct{}, stoppers)
	results := make(chan error, stoppers)
	for range stoppers {
		go func() {
			entered <- struct{}{}
			results <- service.Stop()
		}()
	}
	for range stoppers {
		<-entered
	}
	close(release)

	for i := range stoppers {
		select {
		case err := <-results:
			require.ErrorIs(t, err, runnerErr, "Stop #%d", i+1)
		case <-time.After(5 * time.Second):
			t.Fatalf("Stop #%d did not return after the runner exited", i+1)
		}
	}
}

func TestACDCService_StopAfterACleanExitBeforeReadinessReturnsTheStartupError(t *testing.T) {
	flags := NewTestFlags(t, CreateTestContentDir(t, nil), nil)
	service := NewACDCService("clean-exit-before-ready", flags)
	acdc := service.(*acdcService)
	acdc.StopDelay = time.Minute
	acdc.runner = func(context.Context, app.RunParams, *pflag.FlagSet, string) error {
		return nil
	}

	_, startErr := service.Start()
	require.Error(t, startErr)

	for attempt := 1; attempt <= 2; attempt++ {
		require.EqualError(t, stopWithin(t, service, 2*time.Second), startErr.Error(), "Stop #%d", attempt)
	}
}

func TestACDCService_StopWithoutStartIsBoundedByTheStopDelay(t *testing.T) {
	service := NewACDCService("stop-without-start", NewTestFlags(t, CreateTestContentDir(t, nil), nil))
	service.(*acdcService).StopDelay = 50 * time.Millisecond

	require.Error(t, stopWithin(t, service, 2*time.Second))
}

func stopWithin(t *testing.T, service Service, bound time.Duration) error {
	t.Helper()
	stopped := make(chan error, 1)
	go func() { stopped <- service.Stop() }()
	select {
	case err := <-stopped:
		return err
	case <-time.After(bound):
		t.Fatalf("Stop did not return within %v", bound)
		return nil
	}
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

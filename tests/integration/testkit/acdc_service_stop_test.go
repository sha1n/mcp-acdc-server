package testkit

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestACDCService_StopEndsAnOpenStream(t *testing.T) {
	flags := NewTestFlags(t, CreateTestContentDir(t, nil), nil)
	service := NewACDCService("stop-open-stream", flags)
	acdc := service.(*acdcService)
	acdc.StopDelay = 200 * time.Millisecond

	var serverCtx context.Context
	runnerReturned := make(chan struct{})
	acdc.runner = func(ctx context.Context, params app.RunParams, flags *pflag.FlagSet, binary identity.Binary) error {
		defer close(runnerReturned)
		serverCtx = ctx
		return app.RunWithDeps(ctx, params, flags, binary)
	}

	props, err := service.Start()
	require.NoError(t, err)

	// An event stream stays in flight for as long as its client holds it, so only
	// the app's shutdown ending it lets Stop finish within the StopDelay.
	stream, err := net.Dial("tcp", strings.TrimPrefix(props["acdc.baseURL"].(string), "http://"))
	require.NoError(t, err)
	defer func() { _ = stream.Close() }()
	_, err = stream.Write([]byte("GET /sse HTTP/1.1\r\nHost: localhost\r\n\r\n"))
	require.NoError(t, err)
	_, err = stream.Read(make([]byte, 1))
	require.NoError(t, err, "the server must be serving the stream before it is stopped")

	require.NoError(t, service.Stop())

	select {
	case <-runnerReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("the server run did not return after Stop")
	}
	require.Error(t, serverCtx.Err(), "Stop must cancel the server context")

	require.NoError(t, stream.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, readErr := readUntilError(stream)
	require.False(t, errors.Is(readErr, os.ErrDeadlineExceeded),
		"Stop must end the open stream, got %v", readErr)
}

func TestACDCService_StopAfterAFailedStartReturnsTheStartupErrorPromptly(t *testing.T) {
	startupErr := errors.New("startup failed")
	flags := NewTestFlags(t, CreateTestContentDir(t, nil), nil)
	service := NewACDCService("stop-after-failed-start", flags)
	acdc := service.(*acdcService)
	acdc.StopDelay = time.Minute
	acdc.runner = func(context.Context, app.RunParams, *pflag.FlagSet, identity.Binary) error {
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
	acdc.runner = func(context.Context, app.RunParams, *pflag.FlagSet, identity.Binary) error {
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
	acdc.runner = func(context.Context, app.RunParams, *pflag.FlagSet, identity.Binary) error {
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

func TestACDCService_StopThatTimesOutReleasesTheWatcherAndSharesTheTimeout(t *testing.T) {
	service := NewACDCService("stop-timeout-shared", NewTestFlags(t, CreateTestContentDir(t, nil), nil))
	acdc := service.(*acdcService)
	acdc.StopDelay = 50 * time.Millisecond

	firstErr := stopWithin(t, service, 2*time.Second)
	require.Error(t, firstErr)

	select {
	case <-acdc.watcherDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the runner watcher is still running after Stop gave up waiting")
	}

	acdc.StopDelay = time.Hour
	require.Equal(t, firstErr, stopWithin(t, service, 2*time.Second), "a later Stop must return the shared timeout without waiting again")
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

func TestACDCService_StopOverlappingAStartReturnsTheStartupError(t *testing.T) {
	cases := []struct {
		name      string
		stopDelay time.Duration
		runner    func(ctx context.Context, release <-chan struct{}) error
	}{
		{
			name:      "runner exits cleanly when Stop cancels it",
			stopDelay: time.Minute,
			runner: func(ctx context.Context, _ <-chan struct{}) error {
				<-ctx.Done()
				return nil
			},
		},
		{
			name:      "runner is abandoned by Stop",
			stopDelay: 50 * time.Millisecond,
			runner: func(_ context.Context, release <-chan struct{}) error {
				<-release
				return nil
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			service := NewACDCService("stop-overlapping-start", NewTestFlags(t, CreateTestContentDir(t, nil), nil))
			acdc := service.(*acdcService)
			acdc.StopDelay = c.stopDelay
			entered := make(chan struct{})
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			acdc.runner = func(ctx context.Context, _ app.RunParams, _ *pflag.FlagSet, _ identity.Binary) error {
				close(entered)
				return c.runner(ctx, release)
			}

			started := make(chan error, 1)
			go func() {
				_, err := service.Start()
				started <- err
			}()
			<-entered

			stopErr := stopWithin(t, service, 2*time.Second)

			var startErr error
			select {
			case startErr = <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("Start did not return after Stop")
			}
			require.Error(t, startErr)
			require.EqualError(t, stopErr, startErr.Error(), "Stop #1")
			require.Same(t, stopErr, stopWithin(t, service, 2*time.Second), "Stop #2")
		})
	}
}

func TestACDCService_StopAfterAReadinessTimeoutReturnsTheStartupError(t *testing.T) {
	cases := []struct {
		name   string
		runner func(ctx context.Context, release <-chan struct{}) error
	}{
		{
			name: "runner returns the cancellation",
			runner: func(ctx context.Context, _ <-chan struct{}) error {
				<-ctx.Done()
				return ctx.Err()
			},
		},
		{
			name: "runner exits cleanly after the timeout",
			runner: func(_ context.Context, release <-chan struct{}) error {
				<-release
				return nil
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			service := NewACDCService("stop-after-readiness-timeout", NewTestFlags(t, CreateTestContentDir(t, nil), nil))
			acdc := service.(*acdcService)
			acdc.StopDelay = time.Minute
			acdc.StartTimeout = 50 * time.Millisecond
			release := make(chan struct{})
			acdc.runner = func(ctx context.Context, _ app.RunParams, _ *pflag.FlagSet, _ identity.Binary) error {
				return c.runner(ctx, release)
			}

			_, startErr := service.Start()
			require.Error(t, startErr)
			close(release)

			for attempt := 1; attempt <= 2; attempt++ {
				require.EqualError(t, stopWithin(t, service, 2*time.Second), startErr.Error(), "Stop #%d", attempt)
			}
		})
	}
}

func TestACDCService_StopsShareOneShutdownResultWhileAStreamIsOpen(t *testing.T) {
	cases := []struct {
		name  string
		stops func(t *testing.T, service Service) [2]error
	}{
		{
			name: "repeated",
			stops: func(t *testing.T, service Service) [2]error {
				return [2]error{stopWithin(t, service, 5*time.Second), stopWithin(t, service, 5*time.Second)}
			},
		},
		{
			name: "concurrent",
			stops: func(t *testing.T, service Service) [2]error {
				entered := make(chan struct{}, 2)
				results := make(chan error, 2)
				for range 2 {
					go func() {
						entered <- struct{}{}
						results <- service.Stop()
					}()
				}
				<-entered
				<-entered
				var errs [2]error
				for i := range errs {
					select {
					case errs[i] = <-results:
					case <-time.After(5 * time.Second):
						t.Fatalf("Stop #%d did not return", i+1)
					}
				}
				return errs
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			service := NewACDCService("stops-share-shutdown", NewTestFlags(t, CreateTestContentDir(t, nil), nil))
			service.(*acdcService).StopDelay = 200 * time.Millisecond

			props, err := service.Start()
			require.NoError(t, err)
			stream := openEventStream(t, props["acdc.baseURL"].(string))
			defer func() { _ = stream.Close() }()

			errs := c.stops(t, service)

			for i, err := range errs {
				require.NoError(t, err, "Stop #%d", i+1)
			}
		})
	}
}

func TestACDCService_StartAfterStopFails(t *testing.T) {
	service := NewACDCService("start-after-stop", NewTestFlags(t, CreateTestContentDir(t, nil), nil))
	acdc := service.(*acdcService)
	acdc.StopDelay = 50 * time.Millisecond
	ran := make(chan struct{}, 1)
	acdc.runner = func(context.Context, app.RunParams, *pflag.FlagSet, identity.Binary) error {
		ran <- struct{}{}
		return nil
	}
	_ = stopWithin(t, service, 2*time.Second)

	_, err := service.Start()

	require.Error(t, err)
	select {
	case <-ran:
		t.Fatal("Start ran the server after Stop")
	default:
	}
}

func openEventStream(t *testing.T, baseURL string) net.Conn {
	t.Helper()
	stream, err := net.Dial("tcp", strings.TrimPrefix(baseURL, "http://"))
	require.NoError(t, err)
	_, err = stream.Write([]byte("GET /sse HTTP/1.1\r\nHost: localhost\r\n\r\n"))
	require.NoError(t, err)
	_, err = stream.Read(make([]byte, 1))
	require.NoError(t, err, "the server must be serving the stream before it is stopped")
	return stream
}

func TestACDCService_SecondStartFailsAndLeavesTheFirstRunning(t *testing.T) {
	flags := NewTestFlags(t, CreateTestContentDir(t, nil), &FlagOptions{Transport: "stdio"})
	service := NewACDCService("second-start", flags)
	acdc := service.(*acdcService)
	acdc.StopDelay = time.Minute
	runs := make(chan context.Context, 2)
	acdc.runner = func(ctx context.Context, _ app.RunParams, _ *pflag.FlagSet, _ identity.Binary) error {
		runs <- ctx
		<-ctx.Done()
		return ctx.Err()
	}

	_, err := service.Start()
	require.NoError(t, err)
	firstRun := <-runs

	_, err = service.Start()

	require.EqualError(t, err, "second-start was already started")
	select {
	case <-runs:
		t.Fatal("the second Start ran the server again")
	default:
	}
	require.NoError(t, firstRun.Err(), "the second Start must leave the first run alone")
	for attempt := 1; attempt <= 2; attempt++ {
		require.NoError(t, stopWithin(t, service, 2*time.Second), "Stop #%d", attempt)
	}
	require.Error(t, firstRun.Err(), "Stop must still stop the first run")
}

func TestACDCService_ServerPublishedAfterStopIsNeverServed(t *testing.T) {
	flags := NewTestFlags(t, CreateTestContentDir(t, nil), nil)
	service := NewACDCService("late-server", flags)
	acdc := service.(*acdcService)
	acdc.StopDelay = 50 * time.Millisecond
	hookEntered := make(chan struct{})
	releaseHook := make(chan struct{})
	hookReturned := make(chan error, 1)
	acdc.runner = func(ctx context.Context, params app.RunParams, flags *pflag.FlagSet, binary identity.Binary) error {
		startSSEServer := params.StartSSEServer
		params.StartSSEServer = func(sseCtx context.Context, logger *slog.Logger, mcpSrv *mcp.Server, settings *config.Settings, opts ...app.SSEOption) error {
			close(hookEntered)
			<-releaseHook
			err := startSSEServer(sseCtx, logger, mcpSrv, settings, opts...)
			hookReturned <- err
			return err
		}
		return app.RunWithDeps(ctx, params, flags, binary)
	}

	started := make(chan error, 1)
	go func() {
		_, err := service.Start()
		started <- err
	}()
	<-hookEntered

	// Stop has returned, so the run context is cancelled before the server starts.
	stopErr := stopWithin(t, service, 2*time.Second)
	require.Error(t, stopErr)
	close(releaseHook)

	select {
	case err := <-hookReturned:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("a server started after Stop is serving")
	}
	port, _ := flags.GetInt("port")
	conn, dialErr := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
	if dialErr == nil {
		_ = conn.Close()
	}
	require.Error(t, dialErr, "nothing may listen after Stop")
	require.EqualError(t, <-started, stopErr.Error())
	require.Same(t, stopErr, stopWithin(t, service, 2*time.Second))
}

// The app gives an in-flight handler its whole shutdown grace before
// force-closing it, so Stop's default wait must outlast the grace plus the
// force-close and cleanup that follow it.
func TestACDCService_StopReturnsNilWhenAnInFlightRequestHoldsTheShutdownForTheGrace(t *testing.T) {
	service := NewACDCService("stop-in-flight-request", NewTestFlags(t, CreateTestContentDir(t, nil), nil))
	props, err := service.Start()
	require.NoError(t, err)
	openInFlightRequest(t, props["acdc.baseURL"].(string))

	require.NoError(t, stopWithin(t, service, 30*time.Second))
}

func TestACDCService_ConcurrentStopsWhileAStreamIsOpenShareTheRunnerFailure(t *testing.T) {
	runnerErr := errors.New("runner failed")
	service := NewACDCService("concurrent-stops-runner-failure", NewTestFlags(t, CreateTestContentDir(t, nil), nil))
	acdc := service.(*acdcService)
	acdc.StopDelay = time.Minute
	acdc.runner = func(ctx context.Context, params app.RunParams, flags *pflag.FlagSet, binary identity.Binary) error {
		return errors.Join(runnerErr, app.RunWithDeps(ctx, params, flags, binary))
	}
	props, err := service.Start()
	require.NoError(t, err)
	stream := openEventStream(t, props["acdc.baseURL"].(string))
	defer func() { _ = stream.Close() }()

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

	var errs [stoppers]error
	for i := range errs {
		select {
		case errs[i] = <-results:
		case <-time.After(10 * time.Second):
			t.Fatalf("Stop #%d did not return", i+1)
		}
		require.ErrorIs(t, errs[i], runnerErr, "Stop #%d", i+1)
	}
	require.Same(t, errs[0], errs[1], "concurrent Stops must share one result")
}

// openInFlightRequest holds a real handler open until the server force-closes
// its connection. It posts to an /sse session endpoint with
// "Expect: 100-continue" and never sends the body: the server answers
// 100 Continue only once the handler reads the body, so reading that answer
// proves the handler is running, blocked in a read that ignores cancellation.
func openInFlightRequest(t *testing.T, baseURL string) {
	t.Helper()
	addr := strings.TrimPrefix(baseURL, "http://")
	endpoint := openSessionEndpoint(t, addr)
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = io.WriteString(conn, "POST "+endpoint+" HTTP/1.1\r\nHost: "+addr+
		"\r\nContent-Type: application/json\r\nContent-Length: 2\r\nExpect: 100-continue\r\n\r\n")
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	status, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err, "the handler never read the request body")
	require.True(t, strings.HasPrefix(status, "HTTP/1.1 100 "), "got %q, want 100 Continue", status)
}

// openSessionEndpoint opens an /sse stream for the rest of the test and
// returns the request URI of its session's message endpoint.
func openSessionEndpoint(t *testing.T, addr string) string {
	t.Helper()
	stream, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = stream.Close() })
	_, err = io.WriteString(stream, "GET /sse HTTP/1.1\r\nHost: "+addr+"\r\nAccept: text/event-stream\r\n\r\n")
	require.NoError(t, err)
	require.NoError(t, stream.SetReadDeadline(time.Now().Add(5*time.Second)))
	reader := bufio.NewReader(stream)
	for {
		line, err := reader.ReadString('\n')
		require.NoError(t, err, "the /sse stream ended before the endpoint data")
		if data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:"); ok {
			endpoint, err := url.Parse(strings.TrimSpace(data))
			require.NoError(t, err)
			return endpoint.RequestURI()
		}
	}
}

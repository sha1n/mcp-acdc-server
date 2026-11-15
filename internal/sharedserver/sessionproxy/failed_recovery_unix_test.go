//go:build unix

package sessionproxy_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const failedRecoveryPrefix = "failed to recover the shared server connection "

type identityResult struct {
	identity string
	err      error
}

// outageFixture attaches a session proxy to a test listener and, at the
// crash, removes that listener and holds the recovery at its identity
// computation, before the connect wait starts, until the test releases it.
type outageFixture struct {
	t           *testing.T
	host        *proxyHost
	stdoutPipe  *io.PipeWriter
	socketPath  string
	listener    net.Listener
	old         *fakeSharedConn
	logs        *logRecorder
	recomputing chan struct{}
	identity    chan identityResult
}

func startOutage(t *testing.T, configure func(*sessionproxy.Config)) *outageFixture {
	t.Helper()
	dir := proxyTestEndpointDir(t)
	ep, err := endpoint.OpenEndpoint(dir)
	require.NoError(t, err)
	socketPath, err := ep.SocketPath(proxyTestIdentity)
	require.NoError(t, err)
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	t.Cleanup(func() {
		_ = stdinWriter.Close()
		_ = stdoutReader.Close()
	})
	o := &outageFixture{
		t: t, stdoutPipe: stdoutWriter, socketPath: socketPath, listener: listener,
		logs: &logRecorder{}, recomputing: make(chan struct{}, 4), identity: make(chan identityResult, 1),
	}
	var computed atomic.Int32
	var released atomic.Pointer[identityResult]
	cfg := sessionproxy.Config{
		EndpointDir: fixedEndpointDir(dir),
		// Only the first computation of the recovery is held: a re-entry
		// after a drop before the ready line computes the identity again.
		Identity: func() (string, error) {
			switch computed.Add(1) {
			case 1:
				return proxyTestIdentity, nil
			case 2:
				o.recomputing <- struct{}{}
				result := <-o.identity
				released.Store(&result)
			}
			result := released.Load()
			return result.identity, result.err
		},
		ConnectWait: proxyTestDeadline,
		Spawn:       func(string) error { return nil },
		Fallback:    unexpectedFallback(t),
		Logger:      slog.New(o.logs),
		Stdin:       stdinReader,
		Stdout:      stdoutWriter,
	}
	if configure != nil {
		configure(&cfg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	o.host = &proxyHost{t: t, stdin: stdinWriter, stdout: bufio.NewReader(stdoutReader), cancel: cancel, done: make(chan error, 1)}
	go func() { o.host.done <- sessionproxy.Run(ctx, cfg) }()
	t.Cleanup(func() {
		select {
		case o.identity <- identityResult{identity: proxyTestIdentity}:
		default:
		}
		cancel()
		select {
		case err := <-o.host.done:
			if err != nil {
				t.Logf("session proxy ended: %v", err)
			}
		case <-time.After(proxyTestDeadline):
			t.Error("the session proxy did not return after cancellation")
		}
	})
	o.old = o.accept()
	o.old.sendReadyAs("first")
	return o
}

func (o *outageFixture) accept() *fakeSharedConn {
	o.t.Helper()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := o.listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	select {
	case conn := <-accepted:
		o.t.Cleanup(func() { _ = conn.Close() })
		return &fakeSharedConn{t: o.t, conn: conn, lines: bufio.NewReader(conn)}
	case <-time.After(proxyTestDeadline):
		o.t.Fatal("the session proxy never connected")
		return nil
	}
}

// crash leaves no shared server listening and returns once the recovery is
// computing the identity, so host input is held from then on.
func (o *outageFixture) crash() {
	o.t.Helper()
	require.NoError(o.t, o.listener.Close())
	o.old.crash()
	o.awaitRecomputing()
}

func (o *outageFixture) awaitRecomputing() {
	o.t.Helper()
	select {
	case <-o.recomputing:
	case <-time.After(proxyTestDeadline):
		o.t.Fatal("the session proxy never started a recovery")
	}
}

// hold writes lines during the outage and proves each one held before the
// recovery is released.
func (o *outageFixture) hold(lines ...string) {
	o.t.Helper()
	for _, line := range lines {
		o.host.write(line)
	}
	holdBarrier(o.host, request("999"))
}

func (o *outageFixture) release(identity string, err error) {
	o.identity <- identityResult{identity: identity, err: err}
}

// remainingOutput is everything the session proxy wrote to the host after
// the lines already read; it must be called after Run returned.
func (o *outageFixture) remainingOutput() string {
	o.t.Helper()
	require.NoError(o.t, o.stdoutPipe.Close())
	rest, err := io.ReadAll(o.host.stdout)
	require.NoError(o.t, err)
	return string(rest)
}

// requireNoFailureLine pins that the failure reaches stderr only through
// the returned error.
func (o *outageFixture) requireNoFailureLine() {
	o.t.Helper()
	o.logs.mu.Lock()
	defer o.logs.mu.Unlock()
	for _, record := range o.logs.records {
		assert.Less(o.t, record.level, slog.LevelError, "the session proxy logged %q", record.message)
		assert.NotEqual(o.t, recoveredInfo, record.message)
	}
}

// failedRecoveryError checks the failure is the classified recovery error,
// not a session end, and returns it.
func failedRecoveryError(t *testing.T, err error, cause string) error {
	t.Helper()
	require.Error(t, err)
	assert.Contains(t, err.Error(), failedRecoveryPrefix+"("+cause+"): ")
	return err
}

// droppingListener stands in for a replacement shared server that breaks
// every connection before its ready line, by closing it or by sending first
// a line that is not a ready line.
func droppingListener(t *testing.T, socketPath string, firstLine string) {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if firstLine != "" {
				_, _ = io.WriteString(conn, firstLine)
			}
			_ = conn.Close()
		}
	}()
}

func TestRun_FailedRecoveryAnswersEachHeldRequestForEveryCause(t *testing.T) {
	identityErr := errors.New("manifest unreadable")
	endpointErr := errors.New("no endpoint directory")
	spawnErr := errors.New("spawn refused")
	tests := []struct {
		name      string
		cause     string
		configure func(*sessionproxy.Config)
		replace   func(o *outageFixture)
		identity  string
		identErr  error
		wraps     error
	}{
		{name: "identity uncomputable", cause: "identity", identErr: identityErr, wraps: identityErr},
		{name: "endpoint directory unresolvable", cause: "endpoint", wraps: endpointErr, configure: func(cfg *sessionproxy.Config) {
			dir := cfg.EndpointDir
			var calls atomic.Int32
			cfg.EndpointDir = func() (string, error) {
				if calls.Add(1) == 1 {
					return dir()
				}
				return "", endpointErr
			}
		}},
		{name: "socket path unusable", cause: "socket_path", identity: "not-an-identity"},
		{name: "spawn failure", cause: "spawn", wraps: spawnErr, configure: func(cfg *sessionproxy.Config) {
			cfg.Spawn = func(string) error { return spawnErr }
		}},
		{name: "connect wait expired", cause: "connect_wait", wraps: sessionproxy.ErrConnectWaitExpired, configure: func(cfg *sessionproxy.Config) {
			cfg.ConnectWait = time.Second
		}},
		{name: "replacement drops before its ready line twice", cause: "dropped_twice", replace: func(o *outageFixture) {
			droppingListener(o.t, o.socketPath, "")
		}},
		{name: "replacement sends an invalid ready line twice", cause: "dropped_twice", replace: func(o *outageFixture) {
			droppingListener(o.t, o.socketPath, "not a ready line\n")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := startOutage(t, tt.configure)
			o.crash()
			o.hold(request("1"), request(`"two"`))
			if tt.replace != nil {
				tt.replace(o)
			}
			identity := tt.identity
			if identity == "" && tt.identErr == nil {
				identity = proxyTestIdentity
			}
			o.release(identity, tt.identErr)

			assert.Equal(t, crashError("1"), o.host.readLine())
			assert.Equal(t, crashError(`"two"`), o.host.readLine())
			err := failedRecoveryError(t, o.host.result(), tt.cause)
			if tt.wraps != nil {
				assert.ErrorIs(t, err, tt.wraps)
			}
			assert.Empty(t, o.remainingOutput())
			o.requireNoFailureLine()
			assert.Len(t, o.logs.matching(slog.LevelWarn, crashWarning), 1)
			assert.Len(t, o.logs.matching(slog.LevelInfo, attachedMessage), 1, "the recovery never attached")
		})
	}
}

func TestRun_FailedRecoveryAnswersOnlyHeldRequestsInHeldOrder(t *testing.T) {
	o := startOutage(t, nil)
	o.host.write(request("9"))
	require.Equal(t, request("9"), o.old.readLine())
	require.NoError(t, o.listener.Close())
	o.old.crash()
	require.Equal(t, crashError("9"), o.host.readLine(), "the in-flight request is answered at the crash")
	o.awaitRecomputing()

	o.hold(
		request("3"),
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":9}}`+"\n",
		request(`"s"`),
		response("4"),
		`[{"jsonrpc":"2.0","id":5,"method":"ping"}]`+"\n",
		`{"jsonrpc":"2.0","id":null,"method":"ping"}`+"\n",
		`{"jsonrpc":"2.0","id":6,"method":7}`+"\n",
		"not json\n",
		request("3"),
		`{"jsonrpc":"2.0","id":1.50,"method":"ping"}`+"\n",
	)
	o.release("", errors.New("manifest unreadable"))

	// One error per held request, in held order, echoing its id bytes.
	for _, id := range []string{"3", `"s"`, "3", "1.50"} {
		assert.Equal(t, crashError(id), o.host.readLine())
	}
	err := failedRecoveryError(t, o.host.result(), "identity")
	assert.ErrorContains(t, err, "manifest unreadable")
	assert.Empty(t, o.remainingOutput(), "nothing for other held lines, nor for the in-flight request again")
	o.requireNoFailureLine()
}

func TestRun_FailedRecoveryAnswersAHeldLineWhoseSocketWriteFailed(t *testing.T) {
	output := newHeldHostOutput(t)
	o := startOutage(t, func(cfg *sessionproxy.Config) { cfg.Stdout = output })
	o.old.write(`{"jsonrpc":"2.0","method":"notifications/progress"}` + "\n")
	output.awaitEntered(t)
	require.NoError(t, o.listener.Close())
	o.old.crash()
	o.host.write(request("1"))
	output.release <- nil
	o.awaitRecomputing()

	o.release("", errors.New("manifest unreadable"))

	err := failedRecoveryError(t, o.host.result(), "identity")
	assert.Equal(t, crashError("1"), nextOutputLine(t, output), "a line not fully written is held, so it is answered")
	assert.ErrorContains(t, err, "manifest unreadable")
	select {
	case line := <-output.lines:
		t.Fatalf("unexpected host output %q", line)
	default:
	}
}

func nextOutputLine(t *testing.T, output *heldHostOutput) string {
	t.Helper()
	select {
	case line := <-output.lines:
		return line
	case <-time.After(proxyTestDeadline):
		t.Fatal("no line reached the host")
		return ""
	}
}

type failingOutput struct{ err error }

func (f failingOutput) Write([]byte) (int, error) { return 0, f.err }

func TestRun_FailedRecoveryStillFailsWhenTheHostOutputFailsWhileAnswering(t *testing.T) {
	outputErr := errors.New("host stdout closed")
	o := startOutage(t, func(cfg *sessionproxy.Config) { cfg.Stdout = failingOutput{err: outputErr} })
	o.crash()
	o.hold(request("1"), request("2"))
	o.release("", errors.New("manifest unreadable"))

	err := failedRecoveryError(t, o.host.result(), "identity")
	assert.ErrorIs(t, err, outputErr, "the host output error is part of the failure")
	o.requireNoFailureLine()
}

func TestRun_FailedRecoveryStillFailsWhenCancelledWhileAnswering(t *testing.T) {
	output := newHeldHostOutput(t)
	o := startOutage(t, func(cfg *sessionproxy.Config) { cfg.Stdout = output })
	o.crash()
	o.hold(request("1"), request("2"))
	o.release("", errors.New("manifest unreadable"))
	output.awaitEntered(t)

	o.host.cancel()

	err := failedRecoveryError(t, o.host.result(), "identity")
	assert.ErrorIs(t, err, context.Canceled, "the answers were cut short by the cancellation")
}

func TestRun_StdinEOFDuringTheConnectWaitOfARecoveryEndsTheSessionWithoutWaiting(t *testing.T) {
	spawning := make(chan struct{}, 1)
	o := startOutage(t, func(cfg *sessionproxy.Config) {
		cfg.ConnectWait = time.Hour
		cfg.Spawn = func(string) error {
			select {
			case spawning <- struct{}{}:
			default:
			}
			return nil
		}
	})
	o.crash()
	o.hold(request("1"))
	o.release(proxyTestIdentity, nil)
	select {
	case <-spawning:
	case <-time.After(proxyTestDeadline):
		t.Fatal("the recovery never tried to start a shared server")
	}

	require.NoError(t, o.host.stdin.Close())

	require.NoError(t, o.host.result(), "stdin EOF during an outage ends the session")
	assert.Empty(t, o.remainingOutput(), "held requests are not answered when the host has gone")
	o.requireNoFailureLine()
}

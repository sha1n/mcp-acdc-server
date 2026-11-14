//go:build unix

package sessionproxy_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedWarning struct {
	message string
	cause   string
	err     error
}

// warningRecorder keeps every Warn record the session proxy logs.
type warningRecorder struct {
	mu       sync.Mutex
	warnings []recordedWarning
}

func (r *warningRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *warningRecorder) Handle(_ context.Context, record slog.Record) error {
	if record.Level != slog.LevelWarn {
		return nil
	}
	warning := recordedWarning{message: record.Message}
	record.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "cause":
			warning.cause = attr.Value.String()
		case "error":
			warning.err, _ = attr.Value.Any().(error)
		}
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, warning)
	return nil
}

func (r *warningRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }

func (r *warningRecorder) WithGroup(string) slog.Handler { return r }

func (r *warningRecorder) recorded() []recordedWarning {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedWarning(nil), r.warnings...)
}

func (r *warningRecorder) requireOnlyFallbackWarning(t *testing.T, cause string) recordedWarning {
	t.Helper()
	warnings := r.recorded()
	require.Len(t, warnings, 1, "exactly one warning is written")
	assert.Contains(t, warnings[0].message, "per-session server")
	assert.Equal(t, cause, warnings[0].cause)
	return warnings[0]
}

// fallbackServer stands in for the in-process per-session server: it reads
// the handed-over input line by line until it ends, and gives the input up
// when its context ends, as a transport closing on cancellation does.
type fallbackServer struct {
	calls  atomic.Int32
	lines  chan string
	end    chan error
	result error
}

func newFallbackServer() *fallbackServer {
	return &fallbackServer{lines: make(chan string, 64), end: make(chan error, 1)}
}

func (f *fallbackServer) serve(ctx context.Context, input io.ReadCloser) error {
	f.calls.Add(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(input)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				f.lines <- line
			}
			if err != nil {
				f.end <- err
				return
			}
		}
	}()
	select {
	case <-done:
		return f.result
	case <-ctx.Done():
		_ = input.Close()
		<-done
		return nil
	}
}

func (f *fallbackServer) nextLine(t *testing.T) string {
	t.Helper()
	select {
	case line := <-f.lines:
		return line
	case <-time.After(proxyTestDeadline):
		t.Fatal("no line reached the per-session server")
		return ""
	}
}

func (f *fallbackServer) inputEnd(t *testing.T) error {
	t.Helper()
	select {
	case err := <-f.end:
		return err
	case <-time.After(proxyTestDeadline):
		t.Fatal("the handed-over input never ended")
		return nil
	}
}

func startFallingBack(t *testing.T, cfg sessionproxy.Config) (*proxyHost, *warningRecorder, *fallbackServer) {
	t.Helper()
	warnings := &warningRecorder{}
	fallback := newFallbackServer()
	cfg.Logger = slog.New(warnings)
	cfg.Fallback = fallback.serve
	return startProxy(t, cfg), warnings, fallback
}

// writeWithin fails the test instead of hanging when the session proxy does
// not take the host's input.
func writeWithin(t *testing.T, w io.Writer, s string) {
	t.Helper()
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(w, s)
		written <- err
	}()
	select {
	case err := <-written:
		require.NoError(t, err)
	case <-time.After(proxyTestDeadline):
		t.Fatal("the session proxy did not take the host input")
	}
}

func TestRun_FallsBackToAPerSessionServerOnEveryDiscoveryFailure(t *testing.T) {
	identityErr := errors.New("manifest unreadable")
	resolveErr := errors.New("no home directory")
	spawnErr := errors.New("exec failed")
	tests := []struct {
		name          string
		endpointDir   func(t *testing.T) func() (string, error)
		identity      func() (string, error)
		spawn         func(string) error
		connectWait   time.Duration
		wantCause     string
		wantErr       error
		spawnsAllowed bool
	}{
		{
			name:        "identity cannot be computed",
			endpointDir: func(t *testing.T) func() (string, error) { return fixedEndpointDir(proxyTestEndpointDir(t)) },
			identity:    func() (string, error) { return "", identityErr },
			wantCause:   "identity",
			wantErr:     identityErr,
		},
		{
			name: "endpoint directory cannot be resolved",
			endpointDir: func(*testing.T) func() (string, error) {
				return func() (string, error) { return "", resolveErr }
			},
			wantCause: "endpoint",
			wantErr:   resolveErr,
		},
		{
			name:        "endpoint directory is not absolute",
			endpointDir: func(*testing.T) func() (string, error) { return fixedEndpointDir("@runtime/acdc-mcp") },
			wantCause:   "endpoint",
		},
		{
			name: "endpoint directory fails the mode check",
			endpointDir: func(t *testing.T) func() (string, error) {
				dir := proxyTestEndpointDir(t)
				require.NoError(t, os.Mkdir(dir, 0o755))
				require.NoError(t, os.Chmod(dir, 0o755))
				return fixedEndpointDir(dir)
			},
			wantCause: "endpoint",
		},
		{
			name: "socket path exceeds the length limit",
			endpointDir: func(t *testing.T) func() (string, error) {
				return fixedEndpointDir(filepath.Join(proxyTestEndpointDir(t), strings.Repeat("d", 120)))
			},
			wantCause: "socket_path",
			wantErr:   endpoint.ErrSocketPathTooLong,
		},
		{
			name:          "spawn fails",
			endpointDir:   func(t *testing.T) func() (string, error) { return fixedEndpointDir(proxyTestEndpointDir(t)) },
			spawn:         func(string) error { return spawnErr },
			wantCause:     "spawn",
			wantErr:       spawnErr,
			spawnsAllowed: true,
		},
		{
			name:          "connect wait expires",
			endpointDir:   func(t *testing.T) func() (string, error) { return fixedEndpointDir(proxyTestEndpointDir(t)) },
			connectWait:   100 * time.Millisecond,
			wantCause:     "connect_wait",
			wantErr:       sessionproxy.ErrConnectWaitExpired,
			spawnsAllowed: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var spawns atomic.Int32
			spawn := tt.spawn
			if spawn == nil {
				spawn = func(string) error { return nil }
			}
			host, warnings, fallback := startFallingBack(t, sessionproxy.Config{
				EndpointDir: tt.endpointDir(t),
				Identity:    tt.identity,
				ConnectWait: tt.connectWait,
				Spawn:       func(id string) error { spawns.Add(1); return spawn(id) },
			})

			host.write("{\"id\":1}\n")
			assert.Equal(t, "{\"id\":1}\n", fallback.nextLine(t))
			require.NoError(t, host.stdin.Close())

			require.NoError(t, host.result(), "the launch never fails because of server mode")
			assert.ErrorIs(t, fallback.inputEnd(t), io.EOF)
			assert.Equal(t, int32(1), fallback.calls.Load())
			warning := warnings.requireOnlyFallbackWarning(t, tt.wantCause)
			require.Error(t, warning.err)
			if tt.wantErr != nil {
				assert.ErrorIs(t, warning.err, tt.wantErr)
			}
			if !tt.spawnsAllowed {
				assert.Zero(t, spawns.Load(), "no discovery is attempted")
			}
		})
	}
	_, err := os.Stat("@runtime")
	assert.ErrorIs(t, err, os.ErrNotExist, "a non-absolute endpoint directory must never be created")
}

func TestRun_FallsBackWithoutCreatingTheEndpointWhenTheIdentityCannotBeComputed(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	host, warnings, fallback := startFallingBack(t, sessionproxy.Config{
		EndpointDir: fixedEndpointDir(dir),
		Identity:    func() (string, error) { return "", errors.New("manifest unreadable") },
		Spawn:       func(string) error { return nil },
	})
	host.write("{\"id\":1}\n")
	assert.Equal(t, "{\"id\":1}\n", fallback.nextLine(t))
	require.NoError(t, host.stdin.Close())

	require.NoError(t, host.result())
	warnings.requireOnlyFallbackWarning(t, "identity")
	_, err := os.Stat(dir)
	assert.ErrorIs(t, err, os.ErrNotExist, "the endpoint directory is created only for a computed identity")
}

var breakBeforeReadyLine = []struct {
	name      string
	breakConn func(*fakeSharedConn)
}{
	{name: "dropped", breakConn: func(c *fakeSharedConn) { _ = c.conn.Close() }},
	{name: "first line is not a ready line", breakConn: func(c *fakeSharedConn) {
		c.write("{\"jsonrpc\":\"2.0\"}\n")
		c.requireClosedByProxy()
	}},
}

func TestRun_ReentersDiscoveryOnceWhenTheConnectionBreaksBeforeTheReadyLine(t *testing.T) {
	for _, tt := range breakBeforeReadyLine {
		t.Run(tt.name, func(t *testing.T) {
			dir := proxyTestEndpointDir(t)
			server := newFakeSharedServer(t, dir)
			warnings := &warningRecorder{}
			host := startProxy(t, sessionproxy.Config{
				EndpointDir: fixedEndpointDir(dir),
				Spawn:       server.spawn,
				Logger:      slog.New(warnings),
			})
			host.write("{\"id\":1}\n")

			tt.breakConn(server.nextConn())
			conn := server.nextConn()
			conn.sendReady()
			host.write("{\"id\":2}\n")

			assert.Equal(t, "{\"id\":1}\n", conn.readLine())
			assert.Equal(t, "{\"id\":2}\n", conn.readLine())
			conn.write("{\"id\":1,\"result\":{}}\n")
			assert.Equal(t, "{\"id\":1,\"result\":{}}\n", host.readLine())
			assert.Empty(t, warnings.recorded())
		})
	}
}

func TestRun_FallsBackWhenTheConnectionBreaksBeforeTheReadyLineTwice(t *testing.T) {
	for _, tt := range breakBeforeReadyLine {
		t.Run(tt.name, func(t *testing.T) {
			dir := proxyTestEndpointDir(t)
			server := newFakeSharedServer(t, dir)
			host, warnings, fallback := startFallingBack(t, sessionproxy.Config{
				EndpointDir: fixedEndpointDir(dir),
				Spawn:       server.spawn,
			})
			host.write("{\"id\":1}\n")

			tt.breakConn(server.nextConn())
			tt.breakConn(server.nextConn())

			assert.Equal(t, "{\"id\":1}\n", fallback.nextLine(t))
			require.NoError(t, host.stdin.Close())
			require.NoError(t, host.result())
			warnings.requireOnlyFallbackWarning(t, "dropped_twice")
			assert.Equal(t, int32(1), fallback.calls.Load())
			assert.Equal(t, int32(1), server.spawns.Load(), "nothing is spawned after the second break")
		})
	}
}

// gatedSpawn holds discovery in a spawn until the test fails it.
type gatedSpawn struct {
	entered chan struct{}
	fail    chan error
}

func newGatedSpawn() *gatedSpawn {
	return &gatedSpawn{entered: make(chan struct{}, 1), fail: make(chan error, 1)}
}

func (g *gatedSpawn) spawn(string) error {
	g.entered <- struct{}{}
	return <-g.fail
}

func TestRun_HandsHeldInputToTheFallbackAheadOfLiveInput(t *testing.T) {
	gate := newGatedSpawn()
	host, _, fallback := startFallingBack(t, sessionproxy.Config{
		EndpointDir: fixedEndpointDir(proxyTestEndpointDir(t)),
		Spawn:       gate.spawn,
	})
	<-gate.entered
	host.write("{\"id\":1}\n")
	host.write("{\"id\":2}\n")

	gate.fail <- errors.New("exec failed")
	assert.Equal(t, "{\"id\":1}\n", fallback.nextLine(t))
	assert.Equal(t, "{\"id\":2}\n", fallback.nextLine(t))
	host.write("{\"id\":3}\n")
	assert.Equal(t, "{\"id\":3}\n", fallback.nextLine(t))
	require.NoError(t, host.stdin.Close())

	assert.ErrorIs(t, fallback.inputEnd(t), io.EOF)
	require.NoError(t, host.result())
}

func TestRun_KeepsHoldingInputUntilTheFallbackFirstReadsIt(t *testing.T) {
	entered := make(chan io.ReadCloser, 1)
	startRead := make(chan struct{})
	read := make(chan string, 1)
	host := startProxy(t, sessionproxy.Config{
		EndpointDir: fixedEndpointDir(proxyTestEndpointDir(t)),
		Identity:    func() (string, error) { return "", errors.New("manifest unreadable") },
		Spawn:       func(string) error { return nil },
		Fallback: func(_ context.Context, input io.ReadCloser) error {
			entered <- input
			<-startRead
			all, err := io.ReadAll(input)
			read <- string(all)
			return err
		},
	})
	select {
	case <-entered:
	case <-time.After(proxyTestDeadline):
		t.Fatal("the session proxy never fell back")
	}

	writeWithin(t, host.stdin, "{\"id\":1}\n")
	writeWithin(t, host.stdin, "{\"id\":2}\n")
	writeWithin(t, host.stdin, "{\"id\":3")
	require.NoError(t, host.stdin.Close())
	close(startRead)

	select {
	case all := <-read:
		assert.Equal(t, "{\"id\":1}\n{\"id\":2}\n{\"id\":3", all, "an incomplete last line reaches the per-session server as it would today")
	case <-time.After(proxyTestDeadline):
		t.Fatal("the per-session server never read its input to the end")
	}
	require.NoError(t, host.result())
}

func TestRun_HandsAHostReadFailureToTheFallback(t *testing.T) {
	readErr := errors.New("stdin broken")
	host, _, fallback := startFallingBack(t, sessionproxy.Config{
		EndpointDir: fixedEndpointDir(proxyTestEndpointDir(t)),
		Identity:    func() (string, error) { return "", errors.New("manifest unreadable") },
		Spawn:       func(string) error { return nil },
	})
	host.write("{\"id\":1}\n")
	assert.Equal(t, "{\"id\":1}\n", fallback.nextLine(t))

	require.NoError(t, host.stdin.CloseWithError(readErr))

	assert.ErrorIs(t, fallback.inputEnd(t), readErr)
	require.NoError(t, host.result())
}

func TestRun_ReturnsTheFallbackResult(t *testing.T) {
	startupErr := errors.New("failed to load the manifest")
	host := startProxy(t, sessionproxy.Config{
		EndpointDir: fixedEndpointDir(proxyTestEndpointDir(t)),
		Identity:    func() (string, error) { return "", errors.New("manifest unreadable") },
		Spawn:       func(string) error { return nil },
		Fallback:    func(context.Context, io.ReadCloser) error { return startupErr },
	})

	require.ErrorIs(t, host.result(), startupErr)
}

func TestRun_AppliesBackPressureToTheHostOnceTheFallbackReads(t *testing.T) {
	firstRead := make(chan struct{})
	host := startProxy(t, sessionproxy.Config{
		EndpointDir: fixedEndpointDir(proxyTestEndpointDir(t)),
		Identity:    func() (string, error) { return "", errors.New("manifest unreadable") },
		Spawn:       func(string) error { return nil },
		Fallback: func(ctx context.Context, input io.ReadCloser) error {
			_, err := bufio.NewReader(input).ReadString('\n')
			close(firstRead)
			<-ctx.Done()
			return err
		},
	})
	host.write("{\"id\":1}\n")
	<-firstRead
	line := strings.Repeat("x", 1<<20) + "\n"
	written := make(chan error, 1)
	go func() {
		for range 8 {
			if _, err := io.WriteString(host.stdin, line); err != nil {
				written <- err
				return
			}
		}
		written <- nil
	}()

	select {
	case err := <-written:
		t.Fatalf("8 MiB of host input was taken while the per-session server read none of it (write result: %v)", err)
	case <-time.After(noFurtherReadWindow):
	}
	host.cancel()
	require.NoError(t, host.result())
	require.NoError(t, host.stdin.Close())
	assert.ErrorIs(t, <-written, io.ErrClosedPipe)
}

func TestRun_ClosingTheHandedOverInputEndsAPendingRead(t *testing.T) {
	readEnded := make(chan error, 1)
	closeInput := make(chan struct{})
	host := startProxy(t, sessionproxy.Config{
		EndpointDir: fixedEndpointDir(proxyTestEndpointDir(t)),
		Identity:    func() (string, error) { return "", errors.New("manifest unreadable") },
		Spawn:       func(string) error { return nil },
		Fallback: func(_ context.Context, input io.ReadCloser) error {
			go func() {
				<-closeInput
				_ = input.Close()
			}()
			_, err := input.Read(make([]byte, 64))
			readEnded <- err
			return nil
		},
	})

	close(closeInput)

	select {
	case err := <-readEnded:
		assert.Error(t, err)
	case <-time.After(proxyTestDeadline):
		t.Fatal("a read of the closed input never returned")
	}
	require.NoError(t, host.result())
}

func TestRun_StopsConsumingHostInputOnceTheFallbackReturns(t *testing.T) {
	stdin := newScriptedStdin(t)
	served := make(chan string, 1)
	done := runProxy(t, context.Background(), sessionproxy.Config{
		EndpointDir: fixedEndpointDir(proxyTestEndpointDir(t)),
		Stdin:       stdin,
		Identity:    func() (string, error) { return "", errors.New("manifest unreadable") },
		Spawn:       func(string) error { return nil },
		Fallback: func(_ context.Context, input io.ReadCloser) error {
			line, err := bufio.NewReader(input).ReadString('\n')
			served <- line
			return err
		},
	})
	stdin.awaitRead(t)
	stdin.chunks <- "{\"id\":1}\n"
	assert.Equal(t, "{\"id\":1}\n", <-served)
	require.NoError(t, awaitRunResult(t, done))

	// A read already blocked when Run returned may complete; it is the only one.
	select {
	case <-stdin.reads:
		stdin.chunks <- "{\"id\":2}\n"
	case <-time.After(noFurtherReadWindow):
	}
	select {
	case <-stdin.reads:
		t.Fatal("host input is still consumed after the session proxy returned")
	case <-time.After(noFurtherReadWindow):
	}
}

func TestRun_RequiresAFallback(t *testing.T) {
	var identityCalls atomic.Int32
	stdin, hostWriter := io.Pipe()
	t.Cleanup(func() { _ = hostWriter.Close() })

	err := sessionproxy.Run(context.Background(), sessionproxy.Config{
		EndpointDir: fixedEndpointDir(proxyTestEndpointDir(t)),
		Identity:    func() (string, error) { identityCalls.Add(1); return proxyTestIdentity, nil },
		ConnectWait: proxyTestDeadline,
		Spawn:       func(string) error { return nil },
		Logger:      slog.New(slog.DiscardHandler),
		Stdin:       stdin,
		Stdout:      io.Discard,
	})

	require.Error(t, err)
	assert.Zero(t, identityCalls.Load())
}

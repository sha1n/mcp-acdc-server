//go:build unix

package sessionproxy_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	crashWarning    = "Shared server connection lost, recovering"
	recoveredInfo   = "Recovered the shared server connection"
	attachedMessage = "Attached to the shared server"
)

func crashError(id string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"error":{"code":-32000,"message":"shared server connection lost before a response was received"}}` + "\n"
}

func request(id string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"search"}}` + "\n"
}

func response(id string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"result":{}}` + "\n"
}

type loggedRecord struct {
	level   slog.Level
	message string
	attrs   map[string]string
}

// logRecorder keeps every record the session proxy logs, at every level.
type logRecorder struct {
	mu      sync.Mutex
	records []loggedRecord
}

func (r *logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *logRecorder) Handle(_ context.Context, record slog.Record) error {
	logged := loggedRecord{level: record.Level, message: record.Message, attrs: map[string]string{}}
	record.Attrs(func(attr slog.Attr) bool {
		logged.attrs[attr.Key] = attr.Value.String()
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, logged)
	return nil
}

func (r *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }

func (r *logRecorder) WithGroup(string) slog.Handler { return r }

func (r *logRecorder) matching(level slog.Level, message string) []loggedRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []loggedRecord
	for _, record := range r.records {
		if record.level == level && record.message == message {
			out = append(out, record)
		}
	}
	return out
}

func (c *fakeSharedConn) sendReadyAs(session string) {
	c.t.Helper()
	line, err := sharedserver.EncodeReadyLine(session, "/var/log/acdc-"+session+".log")
	require.NoError(c.t, err)
	c.write(string(line))
}

func (c *fakeSharedConn) crash() {
	c.t.Helper()
	require.NoError(c.t, c.conn.Close())
}

type recoveringProxy struct {
	host   *proxyHost
	server *fakeSharedServer
	logs   *logRecorder
}

func startRecoveringProxy(t *testing.T) (*recoveringProxy, *fakeSharedConn) {
	t.Helper()
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	logs := &logRecorder{}
	host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: server.spawn, Logger: slog.New(logs)})
	conn := server.nextConn()
	conn.sendReadyAs("first")
	return &recoveringProxy{host: host, server: server, logs: logs}, conn
}

func TestRun_AnswersAnInFlightRequestWithTheCrashErrorAndRecoversOntoANewSession(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	proxy.host.write(request("1"))
	require.Equal(t, request("1"), old.readLine(), "the old server consumed the request")

	old.crash()

	assert.Equal(t, crashError("1"), proxy.host.readLine())
	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	proxy.host.write(request("2"))
	require.Equal(t, request("2"), recovered.readLine(), "the failed request is never re-sent")
	recovered.write(response("2"))
	assert.Equal(t, response("2"), proxy.host.readLine())

	warnings := proxy.logs.matching(slog.LevelWarn, crashWarning)
	require.Len(t, warnings, 1)
	assert.Equal(t, "1", warnings[0].attrs["in_flight"])
	assert.NotEmpty(t, warnings[0].attrs["error"])
	attaches := proxy.logs.matching(slog.LevelInfo, attachedMessage)
	require.Len(t, attaches, 2, "one attach line per successful attach")
	assert.Equal(t, "first", attaches[0].attrs["session"])
	assert.Equal(t, "second", attaches[1].attrs["session"])
	assert.Equal(t, "/var/log/acdc-second.log", attaches[1].attrs["log"])
	infos := proxy.logs.matching(slog.LevelInfo, recoveredInfo)
	require.Len(t, infos, 1)
	assert.Equal(t, "0", infos[0].attrs["held"])
}

func TestRun_EchoesTheIdOfEachInFlightRequestWithItsOriginalType(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	for _, id := range []string{`"q-1"`, `7`, `2.5`} {
		proxy.host.write(request(id))
		require.Equal(t, request(id), old.readLine())
	}

	old.crash()

	assert.Equal(t, crashError(`"q-1"`), proxy.host.readLine())
	assert.Equal(t, crashError(`7`), proxy.host.readLine())
	assert.Equal(t, crashError(`2.5`), proxy.host.readLine())
}

func TestRun_AnsweredRequestsGetNoCrashErrorAndDuplicateIdsOneEach(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	for _, line := range []string{request("1"), request("5"), request("5")} {
		proxy.host.write(line)
		require.Equal(t, line, old.readLine())
	}
	old.write(response("1") + response("5"))
	require.Equal(t, response("1"), proxy.host.readLine())
	require.Equal(t, response("5"), proxy.host.readLine())

	old.crash()

	assert.Equal(t, crashError("5"), proxy.host.readLine())
	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	proxy.host.write(request("6"))
	require.Equal(t, request("6"), recovered.readLine())
	recovered.write(response("6"))
	assert.Equal(t, response("6"), proxy.host.readLine(), "no further crash error was written")
}

func TestRun_DeliversCompleteLinesBeforeTheCrashErrorsAndDropsTheIncompleteOne(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	for _, id := range []string{"1", "2"} {
		proxy.host.write(request(id))
		require.Equal(t, request(id), old.readLine())
	}

	old.write(response("1") + `{"jsonrpc":"2.0","id":2,"res`)
	old.crash()

	assert.Equal(t, response("1"), proxy.host.readLine())
	assert.Equal(t, crashError("2"), proxy.host.readLine())
	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	proxy.host.write(request("3"))
	require.Equal(t, request("3"), recovered.readLine())
	recovered.write(response("3"))
	assert.Equal(t, response("3"), proxy.host.readLine())
}

func TestRun_DeliversMessagesHeldDuringTheOutageInOrderAndUnfiltered(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	untracked := []string{
		`[{"jsonrpc":"2.0","id":9,"method":"tools/call"}]` + "\n",
		`"not an object"` + "\n",
		`{"jsonrpc":"2.0","method":"notifications/progress"}` + "\n",
	}
	proxy.host.write(request(`"a"`))
	require.Equal(t, request(`"a"`), old.readLine())
	for _, line := range untracked {
		proxy.host.write(line)
		require.Equal(t, line, old.readLine())
	}

	old.crash()
	require.Equal(t, crashError(`"a"`), proxy.host.readLine())
	recovered := proxy.server.nextConn()
	held := []string{
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"a"}}` + "\n",
		`"not an object"` + "\n",
		request("3"),
		request("4"),
	}
	for _, line := range held {
		proxy.host.write(line)
	}
	recovered.sendReadyAs("second")

	for _, line := range held {
		assert.Equal(t, line, recovered.readLine())
	}
	proxy.host.write(request("5"))
	require.Equal(t, request("5"), recovered.readLine(), "the relay resumed after the held messages")
	recovered.write(response("3"))
	assert.Equal(t, response("3"), proxy.host.readLine(), "untracked lines never get a crash error")
	infos := proxy.logs.matching(slog.LevelInfo, recoveredInfo)
	require.Len(t, infos, 1)
	assert.Equal(t, "4", infos[0].attrs["held"])
}

func TestRun_RecoversWhileTheHostIsSilent(t *testing.T) {
	proxy, old := startRecoveringProxy(t)

	old.crash()

	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	proxy.host.write(request("1"))
	assert.Equal(t, request("1"), recovered.readLine())
}

func TestRun_RecoversFromSuccessiveCrashes(t *testing.T) {
	proxy, conn := startRecoveringProxy(t)
	for round := range 4 {
		id := fmt.Sprint(round + 1)
		proxy.host.write(request(id))
		require.Equal(t, request(id), conn.readLine())

		conn.crash()

		require.Equal(t, crashError(id), proxy.host.readLine())
		conn = proxy.server.nextConn()
		conn.sendReadyAs(fmt.Sprintf("session-%d", round))
	}
	proxy.host.write(request("99"))
	require.Equal(t, request("99"), conn.readLine())
	conn.write(response("99"))
	assert.Equal(t, response("99"), proxy.host.readLine())
	assert.Len(t, proxy.logs.matching(slog.LevelWarn, crashWarning), 4)
	assert.Len(t, proxy.logs.matching(slog.LevelInfo, recoveredInfo), 4)
	assert.Len(t, proxy.logs.matching(slog.LevelInfo, attachedMessage), 5)
}

func TestRun_SendsALineTheCrashInterruptedToTheNewServer(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	proxy.host.write(request("1"))
	require.Equal(t, request("1"), old.readLine())
	tooLargeForTheSocketBuffers := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"pad":"` +
		strings.Repeat("x", 4<<20) + `"}}` + "\n"
	proxy.host.write(tooLargeForTheSocketBuffers)
	// The host reader takes the next line only once the forwarder has taken
	// the large one, which the old server, no longer reading, cannot absorb.
	proxy.host.write(request("3"))

	old.crash()

	require.Equal(t, crashError("1"), proxy.host.readLine())
	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	assert.Equal(t, tooLargeForTheSocketBuffers, recovered.readLine())
	assert.Equal(t, request("3"), recovered.readLine())
	recovered.write(response("2"))
	assert.Equal(t, response("2"), proxy.host.readLine(), "a line not fully written is not in flight")
}

func TestRun_RecomputesTheIdentityAtTheStartOfARecovery(t *testing.T) {
	const changedIdentity = "fedcba9876543210fedcba9876543210"
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	ep, err := endpoint.OpenEndpoint(dir)
	require.NoError(t, err)
	changedSocket, err := ep.SocketPath(changedIdentity)
	require.NoError(t, err)
	changed := &fakeSharedServer{t: t, socketPath: changedSocket, accepted: make(chan net.Conn, 4)}
	var computed atomic.Int32
	identity := func() (string, error) {
		if computed.Add(1) == 1 {
			return proxyTestIdentity, nil
		}
		return changedIdentity, nil
	}
	host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Identity: identity, Spawn: server.spawn})
	old := server.nextConn()
	old.sendReady()
	changed.listen()

	old.crash()

	recovered := changed.nextConn()
	recovered.sendReadyAs("second")
	host.write(request("1"))
	assert.Equal(t, request("1"), recovered.readLine())
	assert.Equal(t, int32(2), computed.Load())
}

func TestRun_StdinEOFDuringAnOutageEndsTheSessionWithoutSendingHeldMessages(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	proxy.host.write(request("1"))
	require.Equal(t, request("1"), old.readLine())
	old.crash()
	require.Equal(t, crashError("1"), proxy.host.readLine())
	pending := proxy.server.nextConn()
	proxy.host.write(request("2"))

	require.NoError(t, proxy.host.stdin.Close())

	require.NoError(t, proxy.host.result())
	pending.requireClosedByProxy()
	assert.Len(t, proxy.logs.matching(slog.LevelInfo, recoveredInfo), 0)
}

func TestRun_CancellationDuringAnOutageEndsTheSessionWellInside500ms(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	proxy.host.write(request("1"))
	require.Equal(t, request("1"), old.readLine())
	old.crash()
	require.Equal(t, crashError("1"), proxy.host.readLine())
	pending := proxy.server.nextConn()

	start := time.Now()
	proxy.host.cancel()

	require.NoError(t, proxy.host.result())
	assert.Less(t, time.Since(start), 500*time.Millisecond)
	pending.requireClosedByProxy()
}

// stalledHostOutput is a host that stopped reading its stdout.
type stalledHostOutput struct {
	entered chan struct{}
	once    sync.Once
	release chan struct{}
}

func (o *stalledHostOutput) Write(p []byte) (int, error) {
	o.once.Do(func() { close(o.entered) })
	<-o.release
	return len(p), nil
}

func TestRun_StdinEOFWhileTheHostIgnoresTheCrashErrorEndsTheSession(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	stdin, hostWriter := io.Pipe()
	stdout := &stalledHostOutput{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(stdout.release) })
	done := make(chan error, 1)
	go func() {
		done <- sessionproxy.Run(context.Background(), sessionproxy.Config{
			EndpointDir: fixedEndpointDir(dir),
			Identity:    func() (string, error) { return proxyTestIdentity, nil },
			ConnectWait: proxyTestDeadline,
			Spawn:       server.spawn,
			Logger:      slog.New(slog.DiscardHandler),
			Fallback:    unexpectedFallback(t),
			Stdin:       stdin,
			Stdout:      stdout,
		})
	}()
	old := server.nextConn()
	old.sendReady()
	_, err := io.WriteString(hostWriter, request("1"))
	require.NoError(t, err)
	require.Equal(t, request("1"), old.readLine())
	old.crash()
	select {
	case <-stdout.entered:
	case <-time.After(proxyTestDeadline):
		t.Fatal("the crash error was never written to the host")
	}

	require.NoError(t, hostWriter.Close())

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("the session proxy did not return well inside 500ms")
	}
}

func TestRun_FailsWhenTheHostOutputBreaksWhileAnsweringInFlightRequests(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	stdin, hostWriter := io.Pipe()
	t.Cleanup(func() { _ = hostWriter.Close() })
	outputErr := errors.New("host went away")
	done := make(chan error, 1)
	go func() {
		done <- sessionproxy.Run(context.Background(), sessionproxy.Config{
			EndpointDir: fixedEndpointDir(dir),
			Identity:    func() (string, error) { return proxyTestIdentity, nil },
			ConnectWait: proxyTestDeadline,
			Spawn:       server.spawn,
			Logger:      slog.New(slog.DiscardHandler),
			Fallback:    unexpectedFallback(t),
			Stdin:       stdin,
			Stdout:      brokenHostOutput{err: outputErr},
		})
	}()
	old := server.nextConn()
	old.sendReady()
	_, err := io.WriteString(hostWriter, request("1"))
	require.NoError(t, err)
	require.Equal(t, request("1"), old.readLine())

	old.crash()

	select {
	case err := <-done:
		require.ErrorIs(t, err, outputErr)
	case <-time.After(proxyTestDeadline):
		t.Fatal("the session proxy did not return")
	}
}

func TestRun_FailsWhenRecoveryCannotReachASharedServer(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	identityErr := errors.New("manifest unreadable")
	var computed atomic.Int32
	identity := func() (string, error) {
		if computed.Add(1) == 1 {
			return proxyTestIdentity, nil
		}
		return "", identityErr
	}
	host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Identity: identity, Spawn: server.spawn})
	old := server.nextConn()
	old.sendReady()

	old.crash()

	require.ErrorIs(t, host.result(), identityErr)
}

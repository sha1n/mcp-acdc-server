//go:build unix

package sessionproxy_test

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	resourcesKind = "resources"
	promptsKind   = "prompts"
	listenReissue = "listen re-issue"
)

func notificationSet(kinds []string) string {
	members := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		members = append(members, `"`+kind+`ListChanged":true`)
	}
	return "{" + strings.Join(members, ",") + "}"
}

func listen(id string, kinds ...string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"subscriptions/listen","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"},"notifications":` + notificationSet(kinds) + `}}` + "\n"
}

func acknowledged(id string, kinds ...string) string {
	return `{"jsonrpc":"2.0","method":"notifications/subscriptions/acknowledged","params":{"_meta":{"io.modelcontextprotocol/subscriptionId":` + id + `},"notifications":` + notificationSet(kinds) + `}}` + "\n"
}

func announcement(kind, id string) string {
	return `{"jsonrpc":"2.0","method":"notifications/` + kind + `/list_changed","params":{"_meta":{"io.modelcontextprotocol/subscriptionId":` + id + `}}}` + "\n"
}

func cancelled(id string) string {
	return `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":` + id + `}}` + "\n"
}

// openStream sends a listen request through the proxy to conn and, when the
// server acknowledges it, establishes that the host saw the acknowledgement.
func openStream(t *testing.T, host *proxyHost, conn *fakeSharedConn, line, ack string) {
	t.Helper()
	host.write(line)
	require.Equal(t, line, conn.readLine())
	if ack != "" {
		conn.write(ack)
		require.Equal(t, ack, host.readLine())
	}
}

func TestRun_ReissuesAnOpenListenStreamAndDiscardsItsSecondAcknowledgement(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	openStream(t, proxy.host, old, listen("7", resourcesKind, promptsKind), acknowledged("7", resourcesKind, promptsKind))
	proxy.host.write(request("8"))
	require.Equal(t, request("8"), old.readLine())

	old.crash()

	require.Equal(t, crashError("8"), proxy.host.readLine(), "only the in-flight request is failed")
	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	require.Equal(t, listen("7", resourcesKind, promptsKind), recovered.readLine(), "re-sent verbatim with the same id")
	recovered.write(acknowledged("7", resourcesKind, promptsKind))
	assert.Equal(t, announcement(resourcesKind, "7"), proxy.host.readLine(), "the second acknowledgement is discarded")
	assert.Equal(t, announcement(promptsKind, "7"), proxy.host.readLine())

	proxy.host.write(request("9"))
	require.Equal(t, request("9"), recovered.readLine())
	recovered.write(announcement(resourcesKind, "7") + response("9"))
	assert.Equal(t, announcement(resourcesKind, "7"), proxy.host.readLine(), "a later change is delivered on the same stream")
	assert.Equal(t, response("9"), proxy.host.readLine())
	proxy.logs.awaitRecord(t, slog.LevelInfo, recoveredInfo)
	assert.Len(t, proxy.logs.matching(slog.LevelInfo, recoveredInfo), 1)
}

func TestRun_ForwardsTheAcknowledgementOfAReissuedStreamTheHostNeverSawAcknowledged(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	openStream(t, proxy.host, old, listen(`"s"`, resourcesKind), "")

	old.crash()

	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	require.Equal(t, listen(`"s"`, resourcesKind), recovered.readLine())
	recovered.write(acknowledged(`"s"`, resourcesKind))
	assert.Equal(t, acknowledged(`"s"`, resourcesKind), proxy.host.readLine(), "the host sees exactly one acknowledgement")
	assert.Equal(t, announcement(resourcesKind, `"s"`), proxy.host.readLine())
}

func TestRun_AnnouncesOnlyWhatTheReissuedStreamWasAgreedAndStampsTheServersId(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	openStream(t, proxy.host, old, listen("2.0", resourcesKind, promptsKind), acknowledged("2", resourcesKind, promptsKind))

	old.crash()

	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	require.Equal(t, listen("2.0", resourcesKind, promptsKind), recovered.readLine())
	recovered.write(acknowledged("2", promptsKind))
	assert.Equal(t, announcement(promptsKind, "2"), proxy.host.readLine())
	proxy.host.write(request("9"))
	require.Equal(t, request("9"), recovered.readLine())
	recovered.write(response("9"))
	assert.Equal(t, response("9"), proxy.host.readLine(), "no resources announcement for a stream the new server did not agree it")
}

func TestRun_ForwardsOtherServerLinesDuringTheReissueInServerOrder(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	openStream(t, proxy.host, old, listen("7", resourcesKind), acknowledged("7", resourcesKind))
	openStream(t, proxy.host, old, listen("8", promptsKind), acknowledged("8", promptsKind))

	old.crash()

	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	require.Equal(t, listen("7", resourcesKind), recovered.readLine())
	require.Equal(t, listen("8", promptsKind), recovered.readLine(), "re-issued in send order")
	ended := `{"jsonrpc":"2.0","id":7,"result":{"_meta":{"io.modelcontextprotocol/subscriptionId":7}}}` + "\n"
	recovered.write(acknowledged("8", promptsKind) + announcement(promptsKind, "8") + ended)
	assert.Equal(t, announcement(promptsKind, "8"), proxy.host.readLine())
	assert.Equal(t, ended, proxy.host.readLine(), "the server ended stream 7 during the re-issue")
	assert.Equal(t, announcement(promptsKind, "8"), proxy.host.readLine(), "announced only on the stream still open")
	proxy.host.write(request("9"))
	require.Equal(t, request("9"), recovered.readLine())
	recovered.write(response("9"))
	assert.Equal(t, response("9"), proxy.host.readLine())
}

func TestRun_DoesNotReissueAnEndedStreamAndAnnouncesNothingWithoutAnOpenStream(t *testing.T) {
	tests := []struct {
		name   string
		opened bool
		end    func(t *testing.T, proxy *recoveringProxy, old *fakeSharedConn)
	}{
		{name: "cancelled by the host", opened: true, end: func(t *testing.T, proxy *recoveringProxy, old *fakeSharedConn) {
			proxy.host.write(cancelled("7"))
			require.Equal(t, cancelled("7"), old.readLine())
		}},
		{name: "ended by the server", opened: true, end: func(t *testing.T, proxy *recoveringProxy, old *fakeSharedConn) {
			old.write(response("7"))
			require.Equal(t, response("7"), proxy.host.readLine())
		}},
		{name: "never opened", end: func(*testing.T, *recoveringProxy, *fakeSharedConn) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy, old := startRecoveringProxy(t)
			if tt.opened {
				openStream(t, proxy.host, old, listen("7", resourcesKind, promptsKind), acknowledged("7", resourcesKind, promptsKind))
			}
			tt.end(t, proxy, old)

			old.crash()

			recovered := proxy.server.nextConn()
			recovered.sendReadyAs("second")
			proxy.host.write(request("9"))
			require.Equal(t, request("9"), recovered.readLine(), "nothing is re-issued before the first later line")
			recovered.write(response("9"))
			assert.Equal(t, response("9"), proxy.host.readLine(), "no crash error and no announcement reached the host first")
		})
	}
}

func TestRun_SendsAListenRequestHeldDuringTheOutageOnlyOnceAsAHeldMessage(t *testing.T) {
	o := startOutage(t, nil)
	o.crash()
	recovered := replacementListener(t, o)
	o.hold(listen("7", resourcesKind))

	o.release(proxyTestIdentity, nil)

	conn := recovered.next()
	conn.sendReadyAs("second")
	require.Equal(t, listen("7", resourcesKind), conn.readLine())
	o.host.write(request("999")[1:])
	require.Equal(t, request("999"), conn.readLine(), "the held listen request was not sent a second time")
	conn.write(acknowledged("7", resourcesKind) + response("999"))
	assert.Equal(t, acknowledged("7", resourcesKind), o.host.readLine(), "no announcement precedes its own acknowledgement")
	assert.Equal(t, response("999"), o.host.readLine())
}

func TestRun_FailedRecoveryAnswersAHeldListenRequestButNotAnOpenOne(t *testing.T) {
	o := startOutage(t, nil)
	openStream(t, o.host, o.old, listen("5", resourcesKind), acknowledged("5", resourcesKind))
	o.crash()
	o.hold(listen("6", resourcesKind))

	o.release("", errors.New("manifest unreadable"))

	assert.Equal(t, crashError("6"), o.host.readLine())
	_ = failedRecoveryError(t, o.host.result(), "identity")
	assert.Empty(t, o.remainingOutput(), "the open stream gets no crash error")
	o.requireNoFailureLine()
}

// replacement listens on the socket path of an outage fixture once its
// first listener is gone, standing in for the recovery's shared server.
type replacement struct {
	t        *testing.T
	accepted chan net.Conn
	count    atomic.Int32
}

func replacementListener(t *testing.T, o *outageFixture) *replacement {
	t.Helper()
	listener, err := net.Listen("unix", o.socketPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	r := &replacement{t: t, accepted: make(chan net.Conn, 4)}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			r.count.Add(1)
			r.accepted <- conn
		}
	}()
	return r
}

func (r *replacement) next() *fakeSharedConn {
	r.t.Helper()
	select {
	case conn := <-r.accepted:
		r.t.Cleanup(func() { _ = conn.Close() })
		return &fakeSharedConn{t: r.t, conn: conn, lines: bufio.NewReader(conn)}
	case <-time.After(proxyTestDeadline):
		r.t.Fatal("the session proxy never connected to the replacement")
		return nil
	}
}

func TestRun_ADropDuringTheListenReissueFailsTheRecovery(t *testing.T) {
	tests := []struct {
		name string
		drop func(t *testing.T, conn *fakeSharedConn)
	}{
		{name: "before the first acknowledgement", drop: func(t *testing.T, conn *fakeSharedConn) {
			require.Equal(t, listen("7", resourcesKind), conn.readLine())
			require.Equal(t, listen("8", promptsKind), conn.readLine())
		}},
		{name: "between two acknowledgements", drop: func(t *testing.T, conn *fakeSharedConn) {
			require.Equal(t, listen("7", resourcesKind), conn.readLine())
			require.Equal(t, listen("8", promptsKind), conn.readLine())
			conn.write(acknowledged("7", resourcesKind))
		}},
		{name: "before the re-issued lines are read", drop: func(*testing.T, *fakeSharedConn) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := startOutage(t, nil)
			openStream(t, o.host, o.old, listen("7", resourcesKind), acknowledged("7", resourcesKind))
			openStream(t, o.host, o.old, listen("8", promptsKind), acknowledged("8", promptsKind))
			o.crash()
			recovered := replacementListener(t, o)
			o.hold(request("1"), `{"jsonrpc":"2.0","method":"notifications/progress"}`+"\n")
			o.release(proxyTestIdentity, nil)

			conn := recovered.next()
			conn.sendReadyAs("second")
			tt.drop(t, conn)
			conn.crash()

			assert.Equal(t, crashError("1"), o.host.readLine())
			err := failedRecoveryError(t, o.host.result(), "dropped_in_sequence")
			assert.ErrorContains(t, err, listenReissue)
			assert.Empty(t, o.remainingOutput(), "no acknowledgement, announcement or answer for the open streams")
			o.requireNoFailureLine()
			assert.Equal(t, int32(1), recovered.count.Load(), "no rediscovery")
			assert.Len(t, o.logs.matching(slog.LevelInfo, attachedMessage), 2)
		})
	}
}

func TestRun_ADropAfterTheLastAcknowledgementIsANewCrash(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	openStream(t, proxy.host, old, listen("7", resourcesKind), acknowledged("7", resourcesKind))
	old.crash()

	second := proxy.server.nextConn()
	second.sendReadyAs("second")
	require.Equal(t, listen("7", resourcesKind), second.readLine())
	second.write(acknowledged("7", resourcesKind))
	second.crash()

	assert.Equal(t, announcement(resourcesKind, "7"), proxy.host.readLine())
	third := proxy.server.nextConn()
	third.sendReadyAs("third")
	require.Equal(t, listen("7", resourcesKind), third.readLine())
	third.write(acknowledged("7", resourcesKind))
	assert.Equal(t, announcement(resourcesKind, "7"), proxy.host.readLine())
	proxy.host.write(request("9"))
	require.Equal(t, request("9"), third.readLine())
	third.write(response("9"))
	assert.Equal(t, response("9"), proxy.host.readLine())
	assert.Len(t, proxy.logs.matching(slog.LevelWarn, crashWarning), 2)
}

func TestRun_StdinEOFWhileAwaitingTheReissuedAcknowledgementEndsTheSession(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	openStream(t, proxy.host, old, listen("7", resourcesKind), acknowledged("7", resourcesKind))
	old.crash()
	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	require.Equal(t, listen("7", resourcesKind), recovered.readLine())

	require.NoError(t, proxy.host.stdin.Close())

	assert.NoError(t, proxy.host.result())
	recovered.requireClosedByProxy()
}

func TestRun_CancellationWhileAwaitingTheReissuedAcknowledgementEndsTheSession(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	openStream(t, proxy.host, old, listen("7", resourcesKind), acknowledged("7", resourcesKind))
	old.crash()
	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	require.Equal(t, listen("7", resourcesKind), recovered.readLine())

	proxy.host.cancel()

	assert.NoError(t, proxy.host.result())
	recovered.requireClosedByProxy()
}

func TestRun_FailsWhenTheHostOutputBreaksDuringTheReissue(t *testing.T) {
	brokenErr := errors.New("host stdout closed")
	o := startOutage(t, func(cfg *sessionproxy.Config) { cfg.Stdout = failingOutput{err: brokenErr} })
	o.host.write(listen("7", resourcesKind))
	require.Equal(t, listen("7", resourcesKind), o.old.readLine())
	o.crash()
	recovered := replacementListener(t, o)
	o.release(proxyTestIdentity, nil)
	conn := recovered.next()
	conn.sendReadyAs("second")
	require.Equal(t, listen("7", resourcesKind), conn.readLine())

	conn.write(acknowledged("7", resourcesKind))

	err := o.host.result()
	require.ErrorIs(t, err, brokenErr)
	assert.NotContains(t, err.Error(), failedRecoveryPrefix)
	conn.requireClosedByProxy()
}

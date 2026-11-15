//go:build unix

package sessionproxy_test

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/require"
)

// sentThrough writes line from the host and returns once conn has read it.
func sentThrough(t *testing.T, host *proxyHost, conn *fakeSharedConn, line string) {
	t.Helper()
	host.write(line)
	require.Equal(t, line, conn.readLine())
}

// heldHostLine holds the first write of one chosen line to the host until
// released, reporting that the write started, and passes every write on.
type heldHostLine struct {
	next    io.Writer
	line    string
	entered chan struct{}
	gate    chan struct{}
	first   sync.Once
	once    sync.Once
}

func holdHostLine(cfg *sessionproxy.Config, line string) *heldHostLine {
	h := &heldHostLine{next: cfg.Stdout, line: line, entered: make(chan struct{}), gate: make(chan struct{})}
	cfg.Stdout = h
	return h
}

func (h *heldHostLine) Write(p []byte) (int, error) {
	if string(p) == h.line {
		h.first.Do(func() {
			close(h.entered)
			<-h.gate
		})
	}
	return h.next.Write(p)
}

func (h *heldHostLine) release() { h.once.Do(func() { close(h.gate) }) }

func (h *heldHostLine) awaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-h.entered:
	case <-time.After(proxyTestDeadline):
		t.Fatal("the session proxy never started the held write to the host")
	}
}

func TestRun_AResponseEndsOnlyTheOldestOccurrenceOfItsIdAcrossRequestKinds(t *testing.T) {
	t.Run("listen first", func(t *testing.T) {
		proxy, old := startRecoveringProxy(t)
		sentThrough(t, proxy.host, old, listen("7", resourcesKind))
		sentThrough(t, proxy.host, old, request("7"))
		sentThrough(t, proxy.host, old, request("8"))
		old.write(response("7"))
		require.Equal(t, response("7"), proxy.host.readLine())

		old.crash()

		require.Equal(t, crashError("7"), proxy.host.readLine(), "the response ended the listen stream, not the newer request")
		require.Equal(t, crashError("8"), proxy.host.readLine())
		recovered := proxy.server.nextConn()
		recovered.sendReadyAs("second")
		proxy.host.write(request("9"))
		require.Equal(t, request("9"), recovered.readLine(), "the ended stream is not re-issued")
	})
	t.Run("ordinary first", func(t *testing.T) {
		proxy, old := startRecoveringProxy(t)
		sentThrough(t, proxy.host, old, request("7"))
		sentThrough(t, proxy.host, old, listen("7", resourcesKind))
		sentThrough(t, proxy.host, old, request("8"))
		old.write(response("7"))
		require.Equal(t, response("7"), proxy.host.readLine())

		old.crash()

		require.Equal(t, crashError("8"), proxy.host.readLine(), "the response answered the older request")
		recovered := proxy.server.nextConn()
		recovered.sendReadyAs("second")
		proxy.host.write(request("9"))
		require.Equal(t, listen("7", resourcesKind), recovered.readLine(), "the newer listen stream is still open")
		recovered.write(acknowledged("7", resourcesKind))
		require.Equal(t, acknowledged("7", resourcesKind), proxy.host.readLine())
		require.Equal(t, announcement(resourcesKind, "7"), proxy.host.readLine())
		require.Equal(t, request("9"), recovered.readLine())
	})
}

func TestRun_ACancellationOfAnOrdinaryRequestKeepsANewerListenStreamWithItsId(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	sentThrough(t, proxy.host, old, request("7"))
	sentThrough(t, proxy.host, old, listen("7", resourcesKind))
	sentThrough(t, proxy.host, old, cancelled("7"))

	old.crash()

	require.Equal(t, crashError("7"), proxy.host.readLine(), "a cancelled request is still answered")
	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	proxy.host.write(request("9"))
	require.Equal(t, listen("7", resourcesKind), recovered.readLine(), "the cancellation targeted the older request")
	recovered.write(acknowledged("7", resourcesKind))
	require.Equal(t, acknowledged("7", resourcesKind), proxy.host.readLine())
	require.Equal(t, announcement(resourcesKind, "7"), proxy.host.readLine())
	require.Equal(t, request("9"), recovered.readLine())
}

func TestRun_AnAcknowledgementHeldOnItsWayToTheHostMarksOnlyTheStreamItWasReadFor(t *testing.T) {
	var held *heldHostLine
	first := listen("7", resourcesKind)
	successor := listen("7", resourcesKind, promptsKind)
	o := startOutage(t, func(cfg *sessionproxy.Config) {
		held = holdHostLine(cfg, acknowledged("7", resourcesKind))
	})
	t.Cleanup(held.release)
	sentThrough(t, o.host, o.old, first)
	o.old.write(acknowledged("7", resourcesKind))
	held.awaitEntered(t)
	sentThrough(t, o.host, o.old, cancelled("7"))
	sentThrough(t, o.host, o.old, successor)
	held.release()
	require.Equal(t, acknowledged("7", resourcesKind), o.host.readLine())

	o.crash()

	recovered := replacementListener(t, o)
	o.release(proxyTestIdentity, nil)
	conn := recovered.next()
	conn.sendReadyAs("second")
	require.Equal(t, successor, conn.readLine(), "only the open successor is re-issued")
	conn.write(acknowledged("7", resourcesKind, promptsKind))
	require.Equal(t, acknowledged("7", resourcesKind, promptsKind), o.host.readLine(), "the host never saw the successor acknowledged")
	require.Equal(t, announcement(resourcesKind, "7"), o.host.readLine())
	require.Equal(t, announcement(promptsKind, "7"), o.host.readLine())
}

func TestRun_TheEndingResponseOfACancelledStreamLeavesItsSuccessorOpen(t *testing.T) {
	first := listen("7", resourcesKind)
	successor := listen("7", resourcesKind, promptsKind)
	tests := []struct {
		name  string
		lines []string
	}{
		{name: "successor opened before the cancellation", lines: []string{first, successor, cancelled("7")}},
		{name: "successor reuses the id after the cancellation", lines: []string{first, cancelled("7"), successor}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy, old := startRecoveringProxy(t)
			for _, line := range tt.lines {
				sentThrough(t, proxy.host, old, line)
			}
			old.write(response("7"))
			require.Equal(t, response("7"), proxy.host.readLine())

			old.crash()

			recovered := proxy.server.nextConn()
			recovered.sendReadyAs("second")
			proxy.host.write(request("9"))
			require.Equal(t, successor, recovered.readLine(), "the response ended the cancelled stream")
			recovered.write(acknowledged("7", promptsKind))
			require.Equal(t, acknowledged("7", promptsKind), proxy.host.readLine())
			require.Equal(t, announcement(promptsKind, "7"), proxy.host.readLine())
			require.Equal(t, request("9"), recovered.readLine())
		})
	}
}

func TestRun_AReissueEndedDuringTheStepLeavesTheAcknowledgementToItsSuccessor(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	first := listen("7", resourcesKind)
	successor := listen("7", promptsKind)
	sentThrough(t, proxy.host, old, first)
	sentThrough(t, proxy.host, old, successor)

	old.crash()

	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	require.Equal(t, first, recovered.readLine())
	require.Equal(t, successor, recovered.readLine())
	recovered.write(response("7") + acknowledged("7", promptsKind))
	require.Equal(t, response("7"), proxy.host.readLine())
	require.Equal(t, acknowledged("7", promptsKind), proxy.host.readLine())
	require.Equal(t, announcement(promptsKind, "7"), proxy.host.readLine(), "the successor's acknowledgement completed the re-issue")
	proxy.host.write(request("9"))
	require.Equal(t, request("9"), recovered.readLine())
	recovered.write(response("9"))
	require.Equal(t, response("9"), proxy.host.readLine())
}

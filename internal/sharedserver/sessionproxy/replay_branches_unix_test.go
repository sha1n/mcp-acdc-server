//go:build unix

package sessionproxy_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attachGate holds the session proxy in its second attach line, after the
// ready line and before the recovery sequence, until the test opens it.
type attachGate struct {
	slog.Handler
	attaches *atomic.Int32
	entered  chan struct{}
	gate     chan struct{}
}

func gateRecoveryAttach() (*attachGate, func(*sessionproxy.Config)) {
	g := &attachGate{attaches: &atomic.Int32{}, entered: make(chan struct{}), gate: make(chan struct{})}
	return g, func(cfg *sessionproxy.Config) {
		g.Handler = cfg.Logger.Handler()
		cfg.Logger = slog.New(g)
	}
}

func (g *attachGate) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == attachedMessage && g.attaches.Add(1) == 2 {
		close(g.entered)
		<-g.gate
	}
	return g.Handler.Handle(ctx, record)
}

func (g *attachGate) awaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(proxyTestDeadline):
		t.Fatal("the session proxy never attached to the replacement")
	}
}

func TestRun_DropBeforeTheInitializeIsReplayedFailsTheRecovery(t *testing.T) {
	gate, configure := gateRecoveryAttach()
	l := startLegacyOutage(t, bothListChanges, configure)

	recovered := l.recoverOnto(request("7"))
	gate.awaitEntered(t)
	recovered.crash()
	close(gate.gate)

	l.requireFailedWithoutRediscovery("dropped_in_sequence")
	assert.Contains(t, l.host.result().Error(), "initialize")
}

// failingHostLine fails the first host write that contains marker.
type failingHostLine struct {
	marker string
	err    error
	next   func([]byte) (int, error)
}

func (f failingHostLine) Write(p []byte) (int, error) {
	if strings.Contains(string(p), f.marker) {
		return 0, f.err
	}
	return f.next(p)
}

func TestRun_HostWriteFailureDuringTheReplayEndsTheSession(t *testing.T) {
	hostErr := errors.New("host stdout closed")
	l := startLegacyOutage(t, bothListChanges, func(cfg *sessionproxy.Config) {
		cfg.Stdout = failingHostLine{marker: "rebuilt", err: hostErr, next: cfg.Stdout.Write}
	})

	recovered := l.recoverOnto(request("7"))
	require.Equal(t, initializeRequest, recovered.readLine())
	recovered.write(serverLogMessage)

	err := l.host.result()
	require.ErrorIs(t, err, hostErr)
	assert.NotContains(t, err.Error(), failedRecoveryPrefix, "a host that cannot be written to is not a failed recovery")
	recovered.requireClosedByProxy()
}

func TestRun_StdinEOFWhileAwaitingAReplayedResponseEndsTheSession(t *testing.T) {
	l := startLegacyOutage(t, bothListChanges, nil)

	recovered := l.recoverOnto(request("7"))
	require.Equal(t, initializeRequest, recovered.readLine())
	l.host.write("\n")
	require.NoError(t, l.host.stdin.Close())

	require.NoError(t, l.host.result())
	assert.Empty(t, l.remainingOutput(), "held requests get no answer at a session end")
	recovered.requireClosedByProxy()
}

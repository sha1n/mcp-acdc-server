//go:build unix

package sessionproxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type established struct {
	att *attachment
	err error
}

func establishAsync(t *testing.T, cfg Config) <-chan established {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan established, 1)
	go func() {
		att, err := establish(ctx, cfg, make(chan struct{}))
		result <- established{att: att, err: err}
	}()
	t.Cleanup(cancel)
	return result
}

func awaitEstablished(t *testing.T, result <-chan established) *attachment {
	t.Helper()
	select {
	case r := <-result:
		require.NoError(t, r.err)
		require.NotNil(t, r.att)
		t.Cleanup(func() { _ = r.att.conn.Close() })
		return r.att
	case <-time.After(supervisionTestDeadline):
		t.Fatal("establish did not return")
		return nil
	}
}

func sendReady(t *testing.T, conn net.Conn) {
	t.Helper()
	line, err := sharedserver.EncodeReadyLine("abcd1234", "/var/log/acdc.log")
	require.NoError(t, err)
	_, err = conn.Write(line)
	require.NoError(t, err)
}

func TestEstablish_RecomputesTheIdentityOnReentry(t *testing.T) {
	dir := sessionEndTestDir(t)
	first := listenFor(t, dir, firstIdentity)
	second := listenFor(t, dir, secondIdentity)
	identities := []string{firstIdentity, secondIdentity}
	var calls atomic.Int32
	result := establishAsync(t, Config{
		EndpointDir: func() (string, error) { return dir, nil },
		Identity:    func() (string, error) { return identities[min(int(calls.Add(1)), 2)-1], nil },
		ConnectWait: supervisionTestDeadline,
		Spawn:       func(string) error { return nil },
	})

	_ = first.nextConn().Close()
	select {
	case conn := <-second.accepted:
		sendReady(t, conn)
	case <-first.accepted:
		t.Fatal("the re-entry reused the identity computed for the first attempt")
	case <-time.After(supervisionTestDeadline):
		t.Fatal("the re-entry never connected")
	}

	awaitEstablished(t, result)
	assert.Equal(t, int32(2), calls.Load())
}

// The ready line is withheld for real time beyond the connect wait, counted
// from the first accept so the first attempt's budget has certainly run out:
// a re-entry charged with the time spent waiting for it would expire at once.
// The connect wait equals the failure watchdog because it is real time here,
// so no dial has to beat a budget shorter than any other wait of the package.
// The listener stays bound, so the re-entry dials into its backlog without a
// spawn.
func TestEstablish_GivesTheReentryAFreshConnectWaitAfterALongReadyWait(t *testing.T) {
	const connectWait = supervisionTestDeadline
	dir := sessionEndTestDir(t)
	listener := listenFor(t, dir, firstIdentity)
	result := establishAsync(t, Config{
		EndpointDir: func() (string, error) { return dir, nil },
		Identity:    func() (string, error) { return firstIdentity, nil },
		ConnectWait: connectWait,
		Spawn:       func(string) error { return nil },
	})

	conn := listener.nextConn()
	firstBudgetSpent := time.Now().Add(connectWait)
	select {
	case r := <-result:
		t.Fatalf("establish returned while the ready line was withheld: %v", r.err)
	case <-time.After(time.Until(firstBudgetSpent)):
	}
	_ = conn.Close()
	select {
	case reentry := <-listener.accepted:
		sendReady(t, reentry)
	case r := <-result:
		t.Fatalf("the re-entry ended before it connected: %v", r.err)
	case <-time.After(supervisionTestDeadline):
		t.Fatal("the re-entry never connected")
	}

	awaitEstablished(t, result)
}

func TestHandedOverInput_DeliversQueuedHeldLinesBeforeAvailableLiveInput(t *testing.T) {
	in := &hostInput{held: [][]byte{[]byte("first\n"), []byte("held\n")}, live: make(chan []byte, 1), ended: make(chan struct{}), stopped: make(chan struct{})}
	reader := in.handOver()
	defer func() { _ = reader.Close() }()
	first := make([]byte, len("first\n"))
	_, err := io.ReadFull(reader, first)
	require.NoError(t, err)

	in.live <- []byte("live\n")

	next := make([]byte, len("held\n"))
	_, err = io.ReadFull(reader, next)
	require.NoError(t, err)
	assert.Equal(t, "held\n", string(next), "live input overtook a held line")
}

type cancellingRecorder struct {
	lines  []string
	after  int
	cancel context.CancelFunc
}

func (r *cancellingRecorder) Write(p []byte) (int, error) {
	r.lines = append(r.lines, string(p))
	if len(r.lines) == r.after {
		r.cancel()
	}
	return len(p), nil
}

func TestHostInputForwardTo_WritesHeldLinesBeforeAvailableLiveInput(t *testing.T) {
	in := &hostInput{held: [][]byte{[]byte("first\n"), []byte("held\n")}, live: make(chan []byte, 1), ended: make(chan struct{}), stopped: make(chan struct{})}
	in.live <- []byte("live\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := &cancellingRecorder{after: 3, cancel: cancel}

	require.NoError(t, in.forwardTo(ctx, recorder))

	assert.Equal(t, []string{"first\n", "held\n", "live\n"}, recorder.lines)
}

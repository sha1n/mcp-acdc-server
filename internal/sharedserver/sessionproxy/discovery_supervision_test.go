package sessionproxy

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const supervisionTestDeadline = 10 * time.Second

// heldOperation blocks a dial or spawn until the test releases it, so the
// test controls exactly when the operation is in progress.
type heldOperation struct {
	entered chan struct{}
	release chan struct{}
	calls   int
}

func newHeldOperation(t *testing.T) *heldOperation {
	op := &heldOperation{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(op.release) })
	return op
}

func (op *heldOperation) spawn() error {
	op.start()
	<-op.release
	return errors.New("late spawn result")
}

func (op *heldOperation) dial(ctx context.Context) (net.Conn, error) {
	op.start()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-op.release:
		return nil, errors.New("connection refused")
	}
}

func (op *heldOperation) start() {
	op.calls++
	if op.calls == 1 {
		close(op.entered)
	}
}

func (op *heldOperation) awaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-op.entered:
	case <-time.After(supervisionTestDeadline):
		t.Fatal("the operation was never started")
	}
}

func connectAsync(t *testing.T, d discovery, ctx context.Context, abort <-chan struct{}) <-chan error {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		conn, err := d.connect(ctx, abort)
		if conn != nil {
			_ = conn.Close()
		}
		result <- err
	}()
	return result
}

func awaitConnect(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(supervisionTestDeadline):
		t.Fatal("discovery did not end")
		return nil
	}
}

func failingDialDiscovery(spawn func() error, c clock, wait time.Duration) discovery {
	d := discoveryFor(&dialScript{failures: 1 << 30}, c, wait)
	d.spawn = spawn
	return d
}

func TestDiscoveryConnect_CancellationEndsASpawnInProgress(t *testing.T) {
	spawn := newHeldOperation(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := connectAsync(t, failingDialDiscovery(spawn.spawn, &stalledClock{now: time.Unix(0, 0)}, time.Second), ctx, nil)
	spawn.awaitEntered(t)

	cancel()

	require.ErrorIs(t, awaitConnect(t, result), context.Canceled)
}

func TestDiscoveryConnect_AbortEndsASpawnInProgress(t *testing.T) {
	spawn := newHeldOperation(t)
	abort := make(chan struct{})
	result := connectAsync(t, failingDialDiscovery(spawn.spawn, &stalledClock{now: time.Unix(0, 0)}, time.Second), context.Background(), abort)
	spawn.awaitEntered(t)

	close(abort)

	require.ErrorIs(t, awaitConnect(t, result), errSessionEnded)
}

func TestDiscoveryConnect_ConnectWaitBoundsASpawnInProgress(t *testing.T) {
	spawn := newHeldOperation(t)
	result := connectAsync(t, failingDialDiscovery(spawn.spawn, realClock{}, 20*time.Millisecond), context.Background(), nil)

	require.ErrorIs(t, awaitConnect(t, result), ErrConnectWaitExpired)
	spawn.awaitEntered(t)
	assert.Equal(t, 1, spawn.calls)
}

func TestDiscoveryConnect_ConnectWaitBoundsADialInProgress(t *testing.T) {
	dial := newHeldOperation(t)
	script := &dialScript{}
	d := discoveryFor(script, realClock{}, 20*time.Millisecond)
	d.dial = dial.dial

	err := awaitConnect(t, connectAsync(t, d, context.Background(), nil))

	require.ErrorIs(t, err, ErrConnectWaitExpired)
	assert.Zero(t, script.spawns, "no candidate is spawned once the connect wait has expired")
}

func TestDiscoveryConnect_AbortCancelsADialInProgress(t *testing.T) {
	dial := newHeldOperation(t)
	d := discoveryFor(&dialScript{}, &stalledClock{now: time.Unix(0, 0)}, time.Second)
	d.dial = dial.dial
	abort := make(chan struct{})
	result := connectAsync(t, d, context.Background(), abort)
	dial.awaitEntered(t)

	close(abort)

	require.ErrorIs(t, awaitConnect(t, result), errSessionEnded)
}

func TestDiscoveryConnect_DoesNotSpawnAfterADialThatEndsAtTheConnectWait(t *testing.T) {
	c := &steppingClock{now: time.Unix(0, 0)}
	script := &dialScript{failures: 1 << 30}
	d := discoveryFor(script, c, time.Second)
	d.dial = func(ctx context.Context) (net.Conn, error) {
		c.now = c.now.Add(time.Second)
		return script.dial(ctx)
	}

	_, err := d.connect(context.Background(), nil)

	require.ErrorIs(t, err, ErrConnectWaitExpired)
	assert.Equal(t, 1, script.dials)
	assert.Zero(t, script.spawns)
}

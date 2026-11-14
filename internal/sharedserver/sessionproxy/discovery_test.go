package sessionproxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type steppingClock struct {
	now   time.Time
	waits []time.Duration
}

func (c *steppingClock) Now() time.Time { return c.now }

// AfterFunc never fires: stepping time moves only through After, so the
// connect wait is reached only at a back-off.
func (c *steppingClock) AfterFunc(time.Duration, func()) func() bool {
	return func() bool { return true }
}

func (c *steppingClock) After(d time.Duration) <-chan time.Time {
	c.waits = append(c.waits, d)
	c.now = c.now.Add(d)
	fired := make(chan time.Time, 1)
	fired <- c.now
	return fired
}

type stalledClock struct{ now time.Time }

func (c *stalledClock) Now() time.Time { return c.now }

func (c *stalledClock) After(time.Duration) <-chan time.Time { return nil }

func (c *stalledClock) AfterFunc(time.Duration, func()) func() bool {
	return func() bool { return true }
}

type dialScript struct {
	failures int
	dials    int
	spawns   int
}

func (s *dialScript) dial(context.Context) (net.Conn, error) {
	s.dials++
	if s.dials <= s.failures {
		return nil, errors.New("connection refused")
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (s *dialScript) spawn() error {
	s.spawns++
	return nil
}

func discoveryFor(script *dialScript, c clock, wait time.Duration) discovery {
	return discovery{
		dial:        script.dial,
		spawn:       script.spawn,
		clock:       c,
		connectWait: wait,
		logger:      slog.New(slog.DiscardHandler),
	}
}

func TestDiscoveryConnect_SpawnsOnEveryFailedDialAndStopsAtTheConnectWait(t *testing.T) {
	script := &dialScript{failures: 1 << 30}
	c := &steppingClock{now: time.Unix(0, 0)}

	conn, err := discoveryFor(script, c, time.Second).connect(context.Background(), nil)

	require.ErrorIs(t, err, ErrConnectWaitExpired)
	assert.Nil(t, conn)
	assert.Equal(t, 6, script.dials)
	assert.Equal(t, script.dials, script.spawns, "a candidate is spawned on every failed dial")
	assert.Equal(t, []time.Duration{
		20 * time.Millisecond,
		40 * time.Millisecond,
		80 * time.Millisecond,
		160 * time.Millisecond,
		320 * time.Millisecond,
		380 * time.Millisecond,
	}, c.waits, "the last back-off is cut to the connect wait")
}

func TestDiscoveryConnect_ReturnsTheFirstSuccessfulDial(t *testing.T) {
	script := &dialScript{failures: 2}
	c := &steppingClock{now: time.Unix(0, 0)}

	conn, err := discoveryFor(script, c, time.Second).connect(context.Background(), nil)

	require.NoError(t, err)
	require.NotNil(t, conn)
	t.Cleanup(func() { _ = conn.Close() })
	assert.Equal(t, 3, script.dials)
	assert.Equal(t, 2, script.spawns)
	assert.Equal(t, []time.Duration{20 * time.Millisecond, 40 * time.Millisecond}, c.waits)
}

func TestDiscoveryConnect_DoesNotSpawnWhenTheFirstDialSucceeds(t *testing.T) {
	script := &dialScript{}
	c := &steppingClock{now: time.Unix(0, 0)}

	conn, err := discoveryFor(script, c, time.Second).connect(context.Background(), nil)

	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	assert.Zero(t, script.spawns)
	assert.Empty(t, c.waits)
}

func TestDiscoveryConnect_SpawnFailureEndsDiscovery(t *testing.T) {
	spawnErr := errors.New("exec failed")
	script := &dialScript{failures: 1 << 30}
	d := discoveryFor(script, &steppingClock{now: time.Unix(0, 0)}, time.Second)
	d.spawn = func() error { return spawnErr }

	_, err := d.connect(context.Background(), nil)

	require.ErrorIs(t, err, spawnErr)
	assert.Equal(t, 1, script.dials)
}

func TestDiscoveryConnect_CancellationEndsTheBackOff(t *testing.T) {
	script := &dialScript{failures: 1 << 30}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := discoveryFor(script, &stalledClock{now: time.Unix(0, 0)}, time.Second).connect(ctx, nil)

	require.ErrorIs(t, err, context.Canceled)
}

func TestDiscoveryConnect_AbortEndsTheBackOff(t *testing.T) {
	script := &dialScript{failures: 1 << 30}
	abort := make(chan struct{})
	close(abort)

	_, err := discoveryFor(script, &stalledClock{now: time.Unix(0, 0)}, time.Second).connect(context.Background(), abort)

	require.ErrorIs(t, err, errSessionEnded)
}

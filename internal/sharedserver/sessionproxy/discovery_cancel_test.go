package sessionproxy

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoveryConnect_CancellationDuringTheBackOffEndsDiscovery(t *testing.T) {
	script := &dialScript{failures: 1 << 30}
	ctx, cancel := context.WithCancel(context.Background())
	d := discoveryFor(script, &stalledClock{now: time.Unix(0, 0)}, time.Second)
	d.spawn = func() error {
		cancel()
		return nil
	}

	_, err := d.connect(ctx, nil)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, script.dials)
}

//go:build unix

package sharedserver

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttach_ARefusedAdmissionClosesTheConnectionWithoutAReadyLineOrLogEntry(t *testing.T) {
	deadline := time.Now().Add(serveTestDeadline)
	a := newAttributedAttach(t)
	require.NoError(t, a.client.SetDeadline(deadline))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	asked := make(chan context.Context, 1)
	go func() {
		defer close(a.done)
		attach(ctx, a.logger, a.serverConn, a.server, func(ctx context.Context) bool {
			asked <- ctx
			return false
		}, "/var/log/acdc.log")
	}()

	received, err := io.ReadAll(a.client)
	require.NoError(t, err, "a refused connection must be closed")
	assert.Empty(t, string(received), "a refused connection must get no ready line")
	select {
	case <-a.done:
	case <-time.After(time.Until(deadline)):
		t.Fatal("attach did not return")
	}
	assert.Equal(t, ctx, <-asked, "admission must be decided in the attach context")
	assert.Empty(t, a.logs.String(), "a refused admission must log nothing")
}

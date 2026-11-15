package sessionproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func endedHostInput(readErr error) *hostInput {
	ended := make(chan struct{})
	close(ended)
	return &hostInput{readErr: readErr, ended: ended, stopped: make(chan struct{})}
}

func relayPipe(t *testing.T) (proxySide, serverSide net.Conn) {
	t.Helper()
	proxySide, serverSide = net.Pipe()
	t.Cleanup(func() {
		_ = proxySide.Close()
		_ = serverSide.Close()
	})
	return proxySide, serverSide
}

func requireClosedByProxy(t *testing.T, serverSide net.Conn) {
	t.Helper()
	// Called only after settle returned: the close already happened, so the
	// read returns at once instead of blocking.
	_, err := serverSide.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF, "the proxy closed the old connection")
}

func TestFinishInput_CancellationEndsTheSessionWithoutWaitingForTheForwarder(t *testing.T) {
	conn, _ := relayPipe(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := finishInput(ctx, conn, make(chan relayEnd), endedHostInput(errors.New("stdin broken")))

	assert.NoError(t, err)
}

func TestFinishInput_IgnoresTheServerDirectionAndReturnsTheForwarderResult(t *testing.T) {
	conn, _ := relayPipe(t)
	forwardErr := errors.New("final relay write failed")
	ends := make(chan relayEnd, 2)
	ends <- relayEnd{err: &connectionLost{err: io.EOF}}
	ends <- relayEnd{toServer: true, err: forwardErr}

	err := finishInput(context.Background(), conn, ends, endedHostInput(io.EOF))

	assert.ErrorIs(t, err, forwardErr, "the server direction ending is not the result of the final relay")
	assert.Empty(t, ends)
}

func TestSessionSettle_SessionEndWhileTheForwarderStopsIsNoCrash(t *testing.T) {
	cases := []struct {
		name string
		end  func(cancel context.CancelFunc, input *hostInput)
	}{
		{name: "cancellation", end: func(cancel context.CancelFunc, _ *hostInput) { cancel() }},
		{name: "host stdin end", end: func(_ context.CancelFunc, input *hostInput) { close(input.ended) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, serverSide := relayPipe(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			input := &hostInput{ended: make(chan struct{}), stopped: make(chan struct{})}
			s := &session{input: input}
			stopped := false
			stop := func() {
				stopped = true
				tc.end(cancel, input)
			}

			crash, err := s.settle(ctx, conn, stop, relayEnd{toServer: true, err: &connectionLost{err: io.EOF}}, make(chan relayEnd))

			assert.Nil(t, crash, "an ended session is not recovered")
			assert.NoError(t, err)
			assert.True(t, stopped, "the forwarder was stopped")
			requireClosedByProxy(t, serverSide)
		})
	}
}

func TestSessionSettle_ResultOfTheSecondDirection(t *testing.T) {
	hostErr := errors.New("host stdout closed")
	cases := []struct {
		name      string
		second    relayEnd
		wantCrash bool
		wantErr   error
	}{
		{name: "host output error", second: relayEnd{err: hostErr}, wantErr: hostErr},
		{name: "connection loss", second: relayEnd{err: &connectionLost{err: io.ErrClosedPipe}}, wantCrash: true},
		{name: "clean end", second: relayEnd{}, wantCrash: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, serverSide := relayPipe(t)
			s := &session{input: &hostInput{ended: make(chan struct{}), stopped: make(chan struct{})}}
			first := &connectionLost{err: io.EOF}
			ends := make(chan relayEnd, 1)
			stop := func() { ends <- tc.second }

			crash, err := s.settle(context.Background(), conn, stop, relayEnd{toServer: true, err: first}, ends)

			if tc.wantCrash {
				assert.Same(t, first, crash, "the first loss is the one crash")
				assert.NoError(t, err)
			} else {
				assert.Nil(t, crash, "a failed host write is not recovered")
				assert.ErrorIs(t, err, tc.wantErr)
			}
			requireClosedByProxy(t, serverSide)
		})
	}
}

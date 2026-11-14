package sessionproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestHostInputForwardTo_RelaysCompleteLinesAndEndsCleanlyOnEOF(t *testing.T) {
	input := readHostInput(strings.NewReader("{\"id\":1}\n{\"id\":2}\n{\"incomplete\""))
	var forwarded bytes.Buffer

	err := input.forwardTo(context.Background(), &forwarded)

	require.NoError(t, err)
	assert.Equal(t, "{\"id\":1}\n{\"id\":2}\n", forwarded.String())
}

func TestHostInputForwardTo_ReportsAHostReadFailure(t *testing.T) {
	readErr := errors.New("stdin broken")
	input := readHostInput(iotest.ErrReader(readErr))

	err := input.forwardTo(context.Background(), &bytes.Buffer{})

	require.ErrorIs(t, err, readErr)
}

func TestHostInputForwardTo_ReportsARelayWriteFailure(t *testing.T) {
	writeErr := errors.New("connection reset")
	input := readHostInput(strings.NewReader("{\"id\":1}\n"))
	<-input.ended

	err := input.forwardTo(context.Background(), failingWriter{err: writeErr})

	require.ErrorIs(t, err, writeErr)
}

func TestHostInputForwardTo_StopsOnCancellation(t *testing.T) {
	stdin, hostWriter := io.Pipe()
	t.Cleanup(func() { _ = hostWriter.Close() })
	input := readHostInput(stdin)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NoError(t, input.forwardTo(ctx, &bytes.Buffer{}))
}

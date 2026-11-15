package sessionproxy

import (
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// answeringConn stands for a shared server that reads a line and has its
// answer relayed to the host before the proxy's write returns, which a unix
// socket allows.
type answeringConn struct {
	net.Conn
	answer func(line []byte)
	err    error
}

func (c answeringConn) Write(line []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	c.answer(line)
	return len(line), nil
}

func TestTrackingWriter_CommitsAnInitializeAnsweredBeforeItsWriteReturned(t *testing.T) {
	s := &session{ledger: newLedger()}
	w := trackingWriter{conn: answeringConn{answer: func([]byte) { s.ledger.deliver(s.ledger.resolve(decodeLine([]byte(eraResult)))) }}, ledger: s.ledger}

	_, err := w.Write([]byte(eraInitialize))
	require.NoError(t, err)

	replay, legacy := s.ledger.replay()
	require.True(t, legacy, "the host saw the initialize succeed")
	assert.Equal(t, eraInitialize, string(replay.initialize))
	assert.Equal(t, eraResult, string(replay.result))
}

func TestTrackingWriter_ForgetsWhatAFailedWriteDidNotDeliver(t *testing.T) {
	s := &session{ledger: newLedger()}
	delivered := trackingWriter{conn: answeringConn{answer: func([]byte) {}}, ledger: s.ledger}
	failed := trackingWriter{conn: answeringConn{err: errors.New("broken pipe")}, ledger: s.ledger}
	_, err := delivered.Write([]byte(eraInitialize))
	require.NoError(t, err)
	s.ledger.deliver(s.ledger.resolve(decodeLine([]byte(eraResult))))
	_, err = delivered.Write([]byte(eraInitialized))
	require.NoError(t, err)

	_, err = failed.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{"retry":true}}` + "\n"))
	require.Error(t, err)
	_, err = failed.Write([]byte(eraSetLevel("5", "debug")))
	require.Error(t, err)
	s.ledger.deliver(s.ledger.resolve(decodeLine([]byte(eraSuccess("5")))))

	replay, legacy := s.ledger.replay()
	require.True(t, legacy)
	assert.Equal(t, eraInitialized, string(replay.initialized), "the initialized in effect is the last one fully written")
	assert.Nil(t, replay.setLevel, "a setLevel whose write failed never reached the server")
}

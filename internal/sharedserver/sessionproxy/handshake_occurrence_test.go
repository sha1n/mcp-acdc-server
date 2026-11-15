package sessionproxy

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ordinaryNine = `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"search"}}` + "\n"

func decoded(line string) decodedLine { return decodeLine([]byte(line)) }

// relayed delivers line to the host as the relay does: resolved before the
// host write, delivered after it.
func relayed(l *ledger, line string) {
	l.deliver(l.resolve(decoded(line)))
}

// legacyLedger is a ledger whose host saw eraInitialize succeed and sent
// eraInitialized.
func legacyLedger(t *testing.T) *ledger {
	t.Helper()
	l := newLedger()
	l.register(decoded(eraInitialize))
	relayed(l, eraResult)
	l.recordInitialized(decoded(eraInitialized))
	_, legacy := l.replay()
	require.True(t, legacy)
	return l
}

func requireReplayedSetLevel(t *testing.T, l *ledger, want string) {
	t.Helper()
	replay, legacy := l.replay()
	require.True(t, legacy)
	assert.Equal(t, eraInitialize, string(replay.initialize))
	assert.Equal(t, eraResult, string(replay.result))
	assert.Equal(t, eraInitialized, string(replay.initialized))
	if want == "" {
		assert.Nil(t, replay.setLevel)
		return
	}
	assert.Equal(t, want, string(replay.setLevel))
}

func TestLedger_CrashDrainReleasesPendingHandshakeOccurrences(t *testing.T) {
	t.Run("an initialize and a setLevel of a modern session", func(t *testing.T) {
		l := newLedger()
		l.register(decoded(eraInitialize))
		l.register(decoded(eraSetLevel("5", "debug")))

		assert.Equal(t, []string{crashErrorLine("0"), crashErrorLine("5")}, lineStrings(l.drain()))
		assert.Equal(t, 0, l.entries.Len(), "no handshake occurrence outlives the crash")

		relayed(l, eraSuccess("0"))
		relayed(l, eraSuccess("5"))
		_, legacy := l.replay()
		assert.False(t, legacy, "a later success with the same id answers another request")
	})
	t.Run("a setLevel of a legacy session keeps only what was committed", func(t *testing.T) {
		l := legacyLedger(t)
		l.register(decoded(eraSetLevel("1", "info")))
		relayed(l, eraSuccess("1"))
		l.register(decoded(eraSetLevel("5", "debug")))

		assert.Equal(t, []string{crashErrorLine("5")}, lineStrings(l.drain()))
		assert.Equal(t, 0, l.entries.Len())

		relayed(l, eraSuccess("5"))
		requireReplayedSetLevel(t, l, eraSetLevel("1", "info"))
	})
}

func TestLedger_TheSynthesizedCrashErrorNeverCommits(t *testing.T) {
	modern := newLedger()
	modern.register(decoded(eraInitialize))
	for _, line := range modern.drain() {
		modern.deliver(modern.resolve(decodeLine(line)))
	}
	_, legacy := modern.replay()
	assert.False(t, legacy)

	l := legacyLedger(t)
	l.register(decoded(eraSetLevel("5", "debug")))
	for _, line := range l.drain() {
		l.deliver(l.resolve(decodeLine(line)))
	}
	requireReplayedSetLevel(t, l, "")
}

func TestLedger_AResponseCommitsTheOldestOutstandingRequestWithItsID(t *testing.T) {
	cases := []struct {
		name      string
		sent      []string
		responses []string
		committed string
	}{
		{
			name: "two setLevel, success then rejection", sent: []string{eraSetLevel("9", "info"), eraSetLevel("9", "debug")},
			responses: []string{eraSuccess("9"), eraFailure("9")}, committed: eraSetLevel("9", "info"),
		},
		{
			name: "two setLevel, rejection then success", sent: []string{eraSetLevel("9", "info"), eraSetLevel("9", "debug")},
			responses: []string{eraFailure("9"), eraSuccess("9")}, committed: eraSetLevel("9", "debug"),
		},
		{
			name: "ordinary then setLevel, success then rejection", sent: []string{ordinaryNine, eraSetLevel("9", "debug")},
			responses: []string{eraSuccess("9"), eraFailure("9")},
		},
		{
			name: "ordinary then setLevel, rejection then success", sent: []string{ordinaryNine, eraSetLevel("9", "debug")},
			responses: []string{eraFailure("9"), eraSuccess("9")}, committed: eraSetLevel("9", "debug"),
		},
		{
			name: "setLevel then ordinary, success then rejection", sent: []string{eraSetLevel("9", "debug"), ordinaryNine},
			responses: []string{eraSuccess("9"), eraFailure("9")}, committed: eraSetLevel("9", "debug"),
		},
		{
			name: "setLevel then ordinary, rejection then success", sent: []string{eraSetLevel("9", "debug"), ordinaryNine},
			responses: []string{eraFailure("9"), eraSuccess("9")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := legacyLedger(t)
			for _, line := range tc.sent {
				l.register(decoded(line))
			}
			for _, line := range tc.responses {
				relayed(l, line)
			}

			requireReplayedSetLevel(t, l, tc.committed)
			assert.Equal(t, 0, l.entries.Len(), "each response ended one occurrence")
		})
	}
}

func TestLedger_ASecondInitializeWithTheSameIDDoesNotTakeTheFirstOnesSuccess(t *testing.T) {
	const again = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n"
	l := newLedger()
	l.register(decoded(eraInitialize))
	l.register(decoded(again))
	relayed(l, eraResult)
	relayed(l, eraFailure("0"))

	replay, legacy := l.replay()
	require.True(t, legacy)
	assert.Equal(t, eraInitialize, string(replay.initialize))
	assert.Equal(t, eraResult, string(replay.result))
}

func TestLedger_AFailedSecondWriteLeavesTheFirstSetLevelToItsResponse(t *testing.T) {
	l := legacyLedger(t)
	delivered := trackingWriter{conn: answeringConn{answer: func([]byte) {}}, ledger: l}
	failed := trackingWriter{conn: answeringConn{err: errors.New("broken pipe")}, ledger: l}
	_, err := delivered.Write([]byte(eraSetLevel("9", "info")))
	require.NoError(t, err)
	_, err = failed.Write([]byte(eraSetLevel("9", "debug")))
	require.Error(t, err)

	relayed(l, eraSuccess("9"))

	requireReplayedSetLevel(t, l, eraSetLevel("9", "info"))
	assert.Equal(t, 0, l.entries.Len(), "the failed write left no occurrence behind")
}

func TestLedger_AResponseResolvedBeforeItsHostWriteCommitsTheRequestItWasReadFor(t *testing.T) {
	l := legacyLedger(t)
	l.register(decoded(eraSetLevel("9", "info")))
	answer := l.resolve(decoded(eraSuccess("9")))
	successor := l.register(decoded(eraSetLevel("9", "debug")))

	l.deliver(answer)

	requireReplayedSetLevel(t, l, eraSetLevel("9", "info"))
	assert.True(t, l.isOutstanding(successor), "the successor awaits its own response")
	relayed(l, eraFailure("9"))
	requireReplayedSetLevel(t, l, eraSetLevel("9", "info"))
	assert.False(t, l.isOutstanding(successor))
}

//go:build unix

package sessionproxy_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func rejection(id string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"error":{"code":-32602,"message":"rejected"}}` + "\n"
}

// roundTrip sends line to conn and relays its success answer to the host.
func roundTrip(t *testing.T, host *proxyHost, conn *fakeSharedConn, line, id string) {
	t.Helper()
	host.write(line)
	require.Equal(t, line, conn.readLine())
	conn.write(response(id))
	require.Equal(t, response(id), host.readLine())
}

// crashLegacyWith crashes the first shared server of l while lines are in
// flight and returns the replacement once the host has their crash errors.
func crashLegacyWith(l *legacyOutage, ids ...string) *fakeSharedConn {
	l.t.Helper()
	require.NoError(l.t, l.listener.Close())
	l.old.crash()
	for _, id := range ids {
		require.Equal(l.t, crashError(id), l.host.readLine())
	}
	l.awaitRecomputing()
	return l.replace()
}

// replayInitialize answers the replayed initialize and initialized of the
// legacy session l on conn.
func replayInitialize(l *legacyOutage, conn *fakeSharedConn) {
	l.t.Helper()
	require.Equal(l.t, initializeRequest, conn.readLine())
	conn.write(l.result)
	require.Equal(l.t, initializedNote, conn.readLine())
}

func TestRun_ReusingTheIDOfAnInitializeFailedByACrashKeepsTheSessionModern(t *testing.T) {
	p, old := startRecoveringProxy(t)
	p.host.write(initializeRequest)
	require.Equal(t, initializeRequest, old.readLine())
	old.crash()
	require.Equal(t, crashError("0"), p.host.readLine())

	second := p.server.nextConn()
	second.sendReadyAs("second")
	roundTrip(t, p.host, second, request("0"), "0")
	second.crash()

	third := p.server.nextConn()
	third.sendReadyAs("third")
	p.host.write(request("7"))
	require.Equal(t, request("7"), third.readLine(), "an initialize the crash failed is never committed by a later response")
}

func TestRun_ReusingTheIDOfASetLevelFailedByACrashDoesNotReplayIt(t *testing.T) {
	l := startLegacyOutage(t, `{}`, nil)
	l.host.write(setLevel("5", "debug"))
	require.Equal(t, setLevel("5", "debug"), l.old.readLine())

	second := crashLegacyWith(l, "5")
	replayInitialize(l, second)
	roundTrip(t, l.host, second, request("5"), "5")
	second.crash()

	third := l.nextReplacement()
	replayInitialize(l, third)
	l.host.write(request("7"))
	require.Equal(t, request("7"), third.readLine(), "a setLevel the crash failed is never committed by a later response")
}

func TestRun_HandshakeRequestsFailedByACrashStayFailedAcrossLaterCrashes(t *testing.T) {
	p, old := startRecoveringProxy(t)
	for _, line := range []string{initializeRequest, setLevel("5", "debug")} {
		p.host.write(line)
		require.Equal(t, line, old.readLine())
	}
	old.crash()
	require.Equal(t, crashError("0"), p.host.readLine())
	require.Equal(t, crashError("5"), p.host.readLine())

	second := p.server.nextConn()
	second.sendReadyAs("second")
	p.host.write(request("3"))
	require.Equal(t, request("3"), second.readLine())
	second.crash()
	require.Equal(t, crashError("3"), p.host.readLine())

	third := p.server.nextConn()
	third.sendReadyAs("third")
	roundTrip(t, p.host, third, request("0"), "0")
	roundTrip(t, p.host, third, request("5"), "5")
	third.crash()

	fourth := p.server.nextConn()
	fourth.sendReadyAs("fourth")
	p.host.write(request("7"))
	require.Equal(t, request("7"), fourth.readLine(), "nothing failed at the first crash is replayed")
}

func TestRun_LegacyRecoveryReplaysTheSetLevelThatSucceededAmongRequestsSharingItsID(t *testing.T) {
	cases := []struct {
		name      string
		sent      []string
		answers   []string
		replayed  string
		rationale string
	}{
		{
			name: "two setLevel, the first succeeds", sent: []string{setLevel("9", "info"), setLevel("9", "debug")},
			answers: []string{response("9"), rejection("9")}, replayed: setLevel("9", "info"),
			rationale: "the first answer is the first setLevel's, the rejected second is not in effect",
		},
		{
			name: "two setLevel, the second succeeds", sent: []string{setLevel("9", "info"), setLevel("9", "debug")},
			answers: []string{rejection("9"), response("9")}, replayed: setLevel("9", "debug"),
		},
		{
			name: "an ordinary request then a setLevel that is rejected", sent: []string{request("9"), setLevel("9", "debug")},
			answers:   []string{response("9"), rejection("9")},
			rationale: "the success answered the ordinary request",
		},
		{
			name: "an ordinary request that is rejected then a setLevel", sent: []string{request("9"), setLevel("9", "debug")},
			answers: []string{rejection("9"), response("9")}, replayed: setLevel("9", "debug"),
			rationale: "the rejection answered the ordinary request",
		},
		{
			name: "a setLevel that succeeds then an ordinary request", sent: []string{setLevel("9", "debug"), request("9")},
			answers: []string{response("9"), rejection("9")}, replayed: setLevel("9", "debug"),
		},
		{
			name: "a rejected setLevel then an ordinary request", sent: []string{setLevel("9", "debug"), request("9")},
			answers:   []string{rejection("9"), response("9")},
			rationale: "the success answered the ordinary request",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := startLegacyOutage(t, `{}`, nil)
			for _, line := range tc.sent {
				l.host.write(line)
				require.Equal(t, line, l.old.readLine())
			}
			for _, answer := range tc.answers {
				l.old.write(answer)
				require.Equal(t, answer, l.host.readLine())
			}

			recovered := l.recoverOnto(request("7"))

			replayInitialize(l, recovered)
			if tc.replayed != "" {
				require.Equal(t, tc.replayed, recovered.readLine(), tc.rationale)
				recovered.write(response("9"))
			}
			require.Equal(t, request("7"), recovered.readLine(), tc.rationale)
		})
	}
}

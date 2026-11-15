package sessionproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func listenLine(id string) []byte {
	return []byte(`{"jsonrpc":"2.0","id":` + id + `,"method":"subscriptions/listen","params":{"notifications":{"resourcesListChanged":true}}}` + "\n")
}

func ackLine(id string) []byte {
	return []byte(`{"jsonrpc":"2.0","method":"notifications/subscriptions/acknowledged","params":{"_meta":{"io.modelcontextprotocol/subscriptionId":` + id + `},"notifications":{"resourcesListChanged":true}}}` + "\n")
}

func cancelLine(id string) []byte {
	return []byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":` + id + `}}` + "\n")
}

func openLines(l *ledger) []string {
	var lines []string
	for _, stream := range l.open() {
		lines = append(lines, string(stream.line))
	}
	return lines
}

func sendListen(t *testing.T, l *ledger, line []byte) {
	t.Helper()
	stream := l.register(decodeLine(line))
	require.True(t, stream != nil && stream.kind == listenRequest, "%s", line)
	l.cancel(l.cancellationTarget(decodeLine(line)))
}

func TestListenStreams_TracksOnlyListenRequestsWithAnId(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
		// ordinary marks a request the ledger tracks as an ordinary
		// occurrence rather than a listen stream.
		ordinary bool
	}{
		{name: "number id", line: string(listenLine("7")), want: true},
		{name: "string id", line: string(listenLine(`"s"`)), want: true},
		{name: "other method", line: `{"jsonrpc":"2.0","id":7,"method":"tools/call"}` + "\n", ordinary: true},
		{name: "no id", line: `{"jsonrpc":"2.0","method":"subscriptions/listen","params":{}}` + "\n"},
		{name: "null id", line: `{"jsonrpc":"2.0","id":null,"method":"subscriptions/listen","params":{}}` + "\n"},
		{name: "method not a string", line: `{"jsonrpc":"2.0","id":7,"method":["subscriptions/listen"]}` + "\n"},
		{name: "batch", line: `[{"jsonrpc":"2.0","id":7,"method":"subscriptions/listen","params":{}}]` + "\n"},
		{name: "not json", line: "subscriptions/listen\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLedger()
			handle := l.register(decodeLine([]byte(tt.line)))
			isListen := handle != nil && handle.kind == listenRequest
			assert.Equal(t, tt.want, isListen)
			if tt.ordinary {
				require.NotNil(t, handle)
				assert.Equal(t, ordinaryRequest, handle.kind)
			} else {
				assert.Equal(t, tt.want, handle != nil)
			}
			if tt.want {
				assert.Equal(t, []string{tt.line}, openLines(l))
			} else {
				assert.Empty(t, openLines(l))
			}
		})
	}
}

func TestListenStreams_OpenStreamsKeepSendOrderAndDuplicates(t *testing.T) {
	l := newLedger()
	for _, id := range []string{"3", `"a"`, "3"} {
		sendListen(t, l, listenLine(id))
	}

	assert.Equal(t, []string{string(listenLine("3")), string(listenLine(`"a"`)), string(listenLine("3"))}, openLines(l))
}

func TestListenStreams_UntrackRemovesOnlyThatStream(t *testing.T) {
	l := newLedger()
	sendListen(t, l, listenLine("1"))
	handle := l.register(decodeLine(listenLine("2")))

	l.undo(handle)
	l.undo(nil)

	assert.Equal(t, []string{string(listenLine("1"))}, openLines(l))
}

func TestListenStreams_AnAcknowledgementDeliveredToTheHostMarksItsStream(t *testing.T) {
	l := newLedger()
	sendListen(t, l, listenLine("1"))
	sendListen(t, l, listenLine(`"two"`))

	l.deliver(l.resolve(decodeLine(ackLine(`"two"`))))

	streams := l.open()
	require.Len(t, streams, 2)
	assert.False(t, streams[0].acknowledged)
	assert.True(t, streams[1].acknowledged)
}

func TestListenStreams_MatchesAnAcknowledgementAsGoSDKStampsTheId(t *testing.T) {
	l := newLedger()
	sendListen(t, l, listenLine("2.0"))

	l.deliver(l.resolve(decodeLine(ackLine("2"))))

	streams := l.open()
	require.Len(t, streams, 1)
	assert.True(t, streams[0].acknowledged)
}

func TestListenStreams_AStringIdDoesNotMatchANumericAcknowledgement(t *testing.T) {
	l := newLedger()
	sendListen(t, l, listenLine(`"1"`))

	l.deliver(l.resolve(decodeLine(ackLine("1"))))

	streams := l.open()
	require.Len(t, streams, 1)
	assert.False(t, streams[0].acknowledged)
}

func TestListenStreams_AHostCancellationEndsTheStream(t *testing.T) {
	l := newLedger()
	sendListen(t, l, listenLine("1"))
	sendListen(t, l, listenLine("2"))

	l.cancel(l.cancellationTarget(decodeLine(cancelLine("1"))))

	assert.Equal(t, []string{string(listenLine("2"))}, openLines(l))
}

func TestListenStreams_ACancellationOfAnotherRequestKeepsTheStream(t *testing.T) {
	l := newLedger()
	sendListen(t, l, listenLine("1"))

	l.cancel(l.cancellationTarget(decodeLine(cancelLine("9"))))
	l.cancel(l.cancellationTarget(decodeLine(cancelLine(`"1"`))))
	l.cancel(l.cancellationTarget(decodeLine([]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled"}` + "\n"))))

	assert.Equal(t, []string{string(listenLine("1"))}, openLines(l))
}

func TestListenStreams_AServerResponseEndsTheStream(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{name: "result", response: `{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/subscriptionId":1}}}` + "\n"},
		{name: "error", response: `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"missing required 'notifications' field"}}` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLedger()
			sendListen(t, l, listenLine("1"))
			sendListen(t, l, listenLine("2"))

			l.deliver(l.resolve(decodeLine([]byte(tt.response))))

			assert.Equal(t, []string{string(listenLine("2"))}, openLines(l))
		})
	}
}

func TestListenStreams_AServerRequestWithTheSameIdKeepsTheStream(t *testing.T) {
	l := newLedger()
	sendListen(t, l, listenLine("1"))

	l.deliver(l.resolve(decodeLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"))))

	assert.Equal(t, []string{string(listenLine("1"))}, openLines(l))
}

func TestListenStreams_DuplicateIdsEndOneStreamPerResponseOldestFirst(t *testing.T) {
	l := newLedger()
	sendListen(t, l, listenLine("1"))
	sendListen(t, l, listenLine("1"))

	l.deliver(l.resolve(decodeLine([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"))))

	assert.Len(t, l.open(), 1)
}

package sessionproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListenStreams_IgnoresServerLinesThatNameNoStream(t *testing.T) {
	for _, line := range []string{
		"not json\n",
		`{"jsonrpc":"2.0","id":null,"result":{}}` + "\n",
		`{"jsonrpc":"2.0","id":1e999,"result":{}}` + "\n",
		`{"jsonrpc":"2.0","method":"notifications/subscriptions/acknowledged","params":[]}` + "\n",
		`{"jsonrpc":"2.0","method":"notifications/subscriptions/acknowledged","params":{"_meta":{}}}` + "\n",
		`{"jsonrpc":"2.0","method":"notifications/subscriptions/acknowledged","params":{"_meta":{"io.modelcontextprotocol/subscriptionId":1e999}}}` + "\n",
		`{"jsonrpc":"2.0","method":"notifications/resources/list_changed","params":{}}` + "\n",
	} {
		t.Run(line, func(t *testing.T) {
			l := newLedger()
			sendListen(t, l, listenLine("1"))

			l.deliver(l.resolve(decodeLine([]byte(line))))

			streams := l.open()
			require.Len(t, streams, 1)
			assert.False(t, streams[0].acknowledged)
		})
	}
}

func TestListenStreams_IgnoresHostCancellationsThatNameNoStream(t *testing.T) {
	for _, line := range []string{
		"not json\n",
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":[]}` + "\n",
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1e999}}` + "\n",
	} {
		t.Run(line, func(t *testing.T) {
			l := newLedger()
			sendListen(t, l, listenLine("1"))

			l.cancel(l.cancellationTarget(decodeLine([]byte(line))))

			assert.Len(t, l.open(), 1)
		})
	}
}

func TestListenStreams_WithoutAnOpenStreamLinesChangeNothing(t *testing.T) {
	l := newLedger()

	l.cancel(l.cancellationTarget(decodeLine(cancelLine("1"))))
	l.deliver(l.resolve(decodeLine(ackLine("1"))))

	assert.Empty(t, l.open())
}

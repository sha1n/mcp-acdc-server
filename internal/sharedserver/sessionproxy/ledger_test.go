package sessionproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLedger_ACommitToARemovedOccurrenceChangesNoOther(t *testing.T) {
	l := newLedger()
	first := l.register(decodeLine(listenLine("7")))
	ended := l.resolve(decodeLine([]byte(`{"jsonrpc":"2.0","id":7,"result":{}}` + "\n")))
	acknowledged := l.resolve(decodeLine(ackLine("7")))
	l.undo(first)
	l.register(decodeLine(listenLine("7")))

	l.deliver(ended)
	l.deliver(acknowledged)
	l.undo(first)

	streams := l.open()
	assert.Len(t, streams, 1, "the successor is still open")
	assert.False(t, streams[0].acknowledged)
	assert.NotSame(t, first, streams[0])
}

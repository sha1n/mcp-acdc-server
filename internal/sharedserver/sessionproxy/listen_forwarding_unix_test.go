//go:build unix

package sessionproxy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_ForwardsALineThatIsNotAnObjectDuringTheReissue(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	openStream(t, proxy.host, old, listen("7", resourcesKind), acknowledged("7", resourcesKind))

	old.crash()

	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	require.Equal(t, listen("7", resourcesKind), recovered.readLine())
	recovered.write("not json\n" + acknowledged("7", resourcesKind))
	assert.Equal(t, "not json\n", proxy.host.readLine())
	assert.Equal(t, announcement(resourcesKind, "7"), proxy.host.readLine())
}

func TestRun_ForwardsAnAcknowledgementOfNoReissuedStreamDuringTheReissue(t *testing.T) {
	proxy, old := startRecoveringProxy(t)
	openStream(t, proxy.host, old, listen("7", resourcesKind), acknowledged("7", resourcesKind))

	old.crash()

	recovered := proxy.server.nextConn()
	recovered.sendReadyAs("second")
	require.Equal(t, listen("7", resourcesKind), recovered.readLine())
	recovered.write(acknowledged("8", resourcesKind) + acknowledged("7", resourcesKind))
	assert.Equal(t, acknowledged("8", resourcesKind), proxy.host.readLine())
	assert.Equal(t, announcement(resourcesKind, "7"), proxy.host.readLine())
}

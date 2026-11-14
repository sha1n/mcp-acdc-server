//go:build unix

package sharedserver

import (
	"sync/atomic"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
)

func TestSessionProxy_ProductionDefaultsKeepEveryStdioLaunchAPerSessionServer(t *testing.T) {
	assert.Nil(t, app.DefaultRunParams().SpawnSharedServer)

	h := testkit.NewSharedServerHarness(t)
	var endpointDirCalls atomic.Int32
	params := app.DefaultRunParams()
	params.EndpointDir = func() (string, error) {
		endpointDirCalls.Add(1)
		return h.EndpointDir, nil
	}
	l := testkit.StartLaunch(t, params, h.Binary, "--content-dir", newSessionProxyContentDir(t))

	connectAndListTools(t, l)

	assert.Zero(t, endpointDirCalls.Load())
	assert.False(t, h.EndpointDirExists())
	assert.Equal(t, 0, h.Spawns())
}

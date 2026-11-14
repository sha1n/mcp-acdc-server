//go:build unix

package sharedserver

import (
	"bytes"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var leadingTimeAttr = regexp.MustCompile(`^time=\S+ `)

func TestSessionProxy_InvalidSettingsFailAsAPerSessionLaunchWithoutDiscovery(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t), "--auth-type", "basic", "--auth-basic-username", "u")
	reference := app.DefaultRunParams()
	reference.EndpointDir = func() (string, error) { return h.EndpointDir, nil }
	reference.LogDir = func() (string, error) { return h.LogDir, nil }

	proxyErr := h.Launch(args...).Wait(testkit.SharedServerDeadline)
	referenceErr := testkit.StartLaunch(t, reference, h.Binary, args...).Wait(testkit.SharedServerDeadline)

	require.Error(t, proxyErr)
	require.Error(t, referenceErr)
	assert.Equal(t, referenceErr.Error(), proxyErr.Error())
	assert.Equal(t, 0, h.Spawns())
	assert.False(t, h.EndpointDirExists())
}

func TestSessionProxy_DumpsTheFullSettingsToStderr(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t),
		"--ui-enabled", "--auth-type", "apikey", "--auth-api-keys", "k1,k2")
	l := h.Launch(args...)

	connectAndListTools(t, l)

	settings, err := config.LoadSettingsWithFlags(testkit.LaunchFlags(t, args))
	require.NoError(t, err)
	var expected bytes.Buffer
	config.LogWithLogger(settings, slog.New(slog.NewTextHandler(&expected, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))
	stderr := l.Stderr()
	var actual []string
	for _, line := range strings.Split(stderr, "\n") {
		actual = append(actual, leadingTimeAttr.ReplaceAllString(line, ""))
	}
	for _, line := range strings.Split(strings.TrimSpace(expected.String()), "\n") {
		assert.Contains(t, actual, line)
	}
	assert.Contains(t, stderr, "auth.type")
	assert.Contains(t, stderr, "auth.api_keys")
	assert.Contains(t, stderr, "Web UI requires the sse transport")
}

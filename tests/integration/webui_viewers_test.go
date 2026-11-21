package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

func TestWebUIViewerHeartbeat_IsAcceptedWithoutEffectOnTheSSEServer(t *testing.T) {
	baseURL, _ := testkit.StartSSE(t,
		&testkit.ContentDirOptions{Resources: webUIFixture()},
		&testkit.FlagOptions{UIEnabled: true})

	resp, err := http.Post(baseURL+"/api/v1/viewers/heartbeat", "application/json", strings.NewReader(`{"viewer_id":"v1"}`)) //nolint:gosec // test server URL
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, http.StatusOK, getJSON(t, baseURL+"/api/v1/config", &map[string]any{}), "the server keeps serving")
}

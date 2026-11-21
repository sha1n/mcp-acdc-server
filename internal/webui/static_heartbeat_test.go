package webui_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStaticPages_LoadCommonScript guards the heartbeat wiring: every page
// counts as a viewer only because it loads common.js, which sends the
// heartbeat.
func TestStaticPages_LoadCommonScript(t *testing.T) {
	for _, page := range []string{"index.html", "doc.html"} {
		t.Run(page, func(t *testing.T) {
			assert.Contains(t, string(readStatic(t, page)), `<script src="/ui/common.js"></script>`)
		})
	}
}

func TestCommonScript_SendsViewerHeartbeat(t *testing.T) {
	src := string(readStatic(t, "common.js"))

	assert.Contains(t, src, `"/api/v1/viewers/heartbeat"`, "posts to the heartbeat endpoint")
	assert.Contains(t, src, `method: "POST"`)
	assert.Contains(t, src, `"Content-Type": "application/json"`)
	assert.Contains(t, src, `viewer_id:`, "sends the viewer id in the body")
	assert.Contains(t, src, "crypto.randomUUID", "uses a random UUID when available")
	assert.Contains(t, src, "HEARTBEAT_INTERVAL_MS = 30000;", "beats about every 30 seconds")
	assert.Contains(t, src, "setInterval(sendHeartbeat, HEARTBEAT_INTERVAL_MS)")
	assert.Contains(t, src, "\n  sendHeartbeat();\n", "beats immediately on load")
}

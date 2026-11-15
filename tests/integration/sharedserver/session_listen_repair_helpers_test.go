//go:build unix

package sharedserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

const (
	resourcesKind = "resources"
	promptsKind   = "prompts"
)

// listenRepairContentDir has both resources and prompts, so a shared server
// agrees to both list-changed notifications on a listen stream.
func listenRepairContentDir(t *testing.T) string {
	t.Helper()
	return testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"auth-guide.md": "---\nname: Authentication Guide\ndescription: Guide for authentication\n---\nThis document covers authentication and security best practices.",
		},
		Prompts: map[string]string{
			"review.md": "---\nname: review\ndescription: Review a change\n---\nReview the change.",
		},
	})
}

// modernListen opens a modern-era listen stream subscribed to both kinds.
func modernListen(id string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"subscriptions/listen","params":{%s,"notifications":{"resourcesListChanged":true,"promptsListChanged":true}}}`,
		id, modernMeta)
}

func cancelRequest(id string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":%s}}`, id)
}

// proxyAnnouncement is the exact line a session proxy writes on a re-issued
// stream after a recovery.
func proxyAnnouncement(kind, subscriptionID string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/%s/list_changed","params":{"_meta":{"%s":%s}}}`+"\n",
		kind, metaKeySubscriptionID, subscriptionID)
}

// hostLine is what a listen-repair test needs to know of a host stdout line.
type hostLine struct {
	raw    string
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Meta          map[string]json.RawMessage `json:"_meta"`
		Notifications map[string]bool            `json:"notifications"`
	} `json:"params"`
	Error *struct {
		Code int `json:"code"`
	} `json:"error"`
}

func decodeHostLine(t *testing.T, line []byte) hostLine {
	t.Helper()
	msg := hostLine{raw: string(line)}
	require.NoError(t, json.Unmarshal(line, &msg), "host line: %s", line)
	return msg
}

func (m hostLine) subscriptionID() string { return string(m.Params.Meta[metaKeySubscriptionID]) }

func (m hostLine) answers(id string) bool { return m.Method == "" && string(m.ID) == id }

func (m hostLine) isAcknowledgement() bool { return m.Method == methodAcknowledged }

// readAcknowledgement reads the next host line and requires it to be the
// acknowledgement of the listen stream id, agreed to both kinds: from then
// on the stream is open.
func readAcknowledgement(t *testing.T, l *testkit.LaunchUnderTest, id string) hostLine {
	t.Helper()
	ack := decodeHostLine(t, l.ReadLine())
	require.True(t, ack.isAcknowledgement(), "the listen stream is acknowledged first: %s", ack.raw)
	require.Equal(t, id, ack.subscriptionID(), "acknowledgement stamped with the stream's id: %s", ack.raw)
	require.Equal(t, map[string]bool{"resourcesListChanged": true, "promptsListChanged": true}, ack.Params.Notifications)
	return ack
}

// addResource changes the content a shared server serves without changing
// its identity.
func addResource(t *testing.T, contentDir string) {
	t.Helper()
	path := filepath.Join(content.NewContentProvider(contentDir).ResourcesDir, "added.md")
	require.NoError(t, os.WriteFile(path, []byte("---\nname: Added\ndescription: Added after recovery\n---\nAdded."), 0o644))
}

// awaitStreamDelivery adds a resource and searches, since the catalog is
// revalidated only on demand, until the shared server delivers a resources
// list-changed notification on the stream listenID. It returns every host
// line read meanwhile, the delivery last.
func awaitStreamDelivery(t *testing.T, l *testkit.LaunchUnderTest, contentDir, listenID string, deadline time.Time) []hostLine {
	t.Helper()
	addResource(t, contentDir)
	var read []hostLine
	for search := 1000; ; search++ {
		require.True(t, time.Now().Before(deadline), "no resources list-changed delivered on stream %s: %v", listenID, read)
		id := fmt.Sprint(search)
		l.WriteLine(modernSearch(id, "added"))
		for {
			msg := decodeHostLine(t, l.ReadLine())
			read = append(read, msg)
			if msg.Method == methodResourcesListChanged && msg.subscriptionID() == listenID {
				return read
			}
			if msg.answers(id) {
				break
			}
		}
		time.Sleep(pollInterval)
	}
}

func rawLines(lines []hostLine) []string {
	raw := make([]string, len(lines))
	for i, l := range lines {
		raw[i] = l.raw
	}
	return raw
}

func listensReceived(received *testkit.ReceivedMessages) int {
	n := 0
	for _, msg := range received.Received() {
		if msg.Method == "subscriptions/listen" {
			n++
		}
	}
	return n
}

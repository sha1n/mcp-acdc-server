//go:build unix

package sharedserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

const (
	rawInitializeRequest    = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"raw-host","version":"1.0.0"}}}`
	rawInitializedNotice    = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	rawToolsListRequestID2  = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	rawToolsListRequestID3  = `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`
	rawSearchCallRequestID3 = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"authentication"}}}`
)

type rawResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func decodeRawResponse(t *testing.T, line []byte) rawResponse {
	t.Helper()
	var resp rawResponse
	require.NoError(t, json.Unmarshal(line, &resp), "line: %s", line)
	return resp
}

func connectAndListTools(t *testing.T, l *testkit.LaunchUnderTest) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()
	session := l.Connect(ctx)
	_, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	return session
}

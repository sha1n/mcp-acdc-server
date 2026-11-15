//go:build unix

package testkit

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// LegacyProtocolVersion is the latest protocol version a client negotiates
// through the initialize handshake, which puts a session in the legacy era.
const LegacyProtocolVersion = "2025-11-25"

// HostTraffic is every byte a host exchanged with a launch, per direction,
// recorded as the host read or wrote it: once a client call has returned,
// every line the launch wrote before its response is recorded.
type HostTraffic struct {
	fromLaunch lockedBuffer
	toLaunch   lockedBuffer
}

// FromLaunch returns the complete lines the host has read from the launch.
func (t *HostTraffic) FromLaunch() []string { return completeLinesOf(t.fromLaunch.String()) }

// ToLaunch returns the complete lines the host has written to the launch.
func (t *HostTraffic) ToLaunch() []string { return completeLinesOf(t.toLaunch.String()) }

func completeLinesOf(text string) []string {
	lines := strings.SplitAfter(text, "\n")
	if last := lines[len(lines)-1]; !strings.HasSuffix(last, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type recordingStdin struct {
	stdin  io.WriteCloser
	record io.Writer
}

func (w recordingStdin) Write(p []byte) (int, error) {
	n, err := w.stdin.Write(p)
	_, _ = w.record.Write(p[:n])
	return n, err
}

func (w recordingStdin) Close() error { return w.stdin.Close() }

// ConnectLegacy initializes an MCP client session over the host streams in
// the legacy era and records the traffic. Closing the session closes host
// stdin.
func (l *LaunchUnderTest) ConnectLegacy(ctx context.Context) (*mcp.ClientSession, *HostTraffic) {
	l.t.Helper()
	traffic := &HostTraffic{}
	transport := &mcp.IOTransport{
		Reader: io.NopCloser(io.TeeReader(l.stdout, &traffic.fromLaunch)),
		Writer: recordingStdin{stdin: l.stdin, record: &traffic.toLaunch},
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "in-process-legacy-host", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, transport, &mcp.ClientSessionOptions{ProtocolVersion: LegacyProtocolVersion})
	require.NoError(l.t, err)
	require.Equal(l.t, LegacyProtocolVersion, session.InitializeResult().ProtocolVersion)
	l.t.Cleanup(func() {
		if err := session.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			l.t.Logf("client session closed with: %v", err)
		}
	})
	return session, traffic
}

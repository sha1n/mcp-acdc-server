//go:build unix

package app

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const browseTestURL = "http://127.0.0.1:4321/ui/"

func browseTestSettings(t *testing.T) *config.Settings {
	t.Helper()
	settings := sessionProxyTestSettings(t, "stdio", config.ServerModeShared)
	settings.Scheme = "acdc"
	settings.Search = config.SearchSettings{
		ResultMode: config.SearchResultModeReferences, KeywordsBoost: 1, HeadingBoost: 1, PathBoost: 1, TitleBoost: 1, ContentBoost: 1,
	}
	settings.SharedServer.Linger = config.MinSharedServerLinger
	settings.UI.MaxResults = 13
	return settings
}

// browseTestServer answers the first browse request with reply once a
// candidate is spawned, and records the hello it received.
type browseTestServer struct {
	t      *testing.T
	dir    string
	reply  []byte
	hellos chan sharedserver.HelloLine
}

func newBrowseTestServer(t *testing.T, reply []byte) *browseTestServer {
	return &browseTestServer{t: t, dir: sessionProxyTestEndpointDir(t), reply: reply, hellos: make(chan sharedserver.HelloLine, 1)}
}

func (s *browseTestServer) spawn(id string) error {
	ep, err := endpoint.OpenEndpoint(s.dir)
	if err != nil {
		return err
	}
	socketPath, err := ep.SocketPath(id)
	if err != nil {
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	s.t.Cleanup(func() { _ = listener.Close() })
	go s.answer(listener)
	return nil
}

func (s *browseTestServer) answer(listener net.Listener) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	hello, err := sharedserver.ReadHelloLine(bufio.NewReader(conn))
	if err == nil {
		s.hellos <- hello
	}
	_, _ = conn.Write(s.reply)
}

func (s *browseTestServer) params(t *testing.T, settings *config.Settings, stdout, stderr *bytes.Buffer) RunParams {
	params := sessionProxyTestParams(t, settings, s.spawn)
	params.EndpointDir = func() (string, error) { return s.dir, nil }
	params.ValidSettings = config.ValidateSettings
	params.Stdout = stdout
	params.Stderr = stderr
	return params
}

func browseReadyReply(t *testing.T) []byte {
	t.Helper()
	line, err := sharedserver.EncodeBrowseReadyLine(browseTestURL, "/var/log/acdc.log")
	require.NoError(t, err)
	return line
}

func TestRunBrowse_PrintsOnlyTheURLOnStdout(t *testing.T) {
	server := newBrowseTestServer(t, browseReadyReply(t))
	var stdout, stderr bytes.Buffer

	err := RunBrowse(context.Background(), server.params(t, browseTestSettings(t), &stdout, &stderr), nil, sessionProxyTestBinary)

	require.NoError(t, err)
	assert.Equal(t, browseTestURL+"\n", stdout.String())
	hello := <-server.hellos
	assert.Equal(t, sharedserver.HelloBrowse, hello.Kind)
	assert.Equal(t, 13, hello.UIMaxResults)
}

func TestRunBrowse_IgnoresUIEnabledHostPortAndAuth(t *testing.T) {
	server := newBrowseTestServer(t, browseReadyReply(t))
	settings := browseTestSettings(t)
	settings.Host, settings.Port = "not a host", -1
	settings.Auth = config.AuthSettings{Type: config.AuthTypeBasic}
	var stdout, stderr bytes.Buffer

	err := RunBrowse(context.Background(), server.params(t, settings, &stdout, &stderr), nil, sessionProxyTestBinary)

	require.NoError(t, err)
	assert.Equal(t, browseTestURL+"\n", stdout.String())
}

func TestRunBrowse_Fails(t *testing.T) {
	errorReply, err := sharedserver.EncodeBrowseErrorLine("failed to start the web UI: address in use", "/var/log/acdc.log")
	require.NoError(t, err)
	tests := []struct {
		name   string
		modify func(*config.Settings)
		reply  []byte
		want   []string
	}{
		{name: "sse transport", modify: func(s *config.Settings) { s.Transport = "sse" }, want: []string{"sse"}},
		{name: "session server mode", modify: func(s *config.Settings) { s.ServerMode = config.ServerModeSession }, want: []string{"shared"}},
		{name: "out of range ui max results", modify: func(s *config.Settings) { s.UI.MaxResults = 0 }, want: []string{"ui.max_results"}},
		{name: "ui bind error", reply: errorReply, want: []string{"address in use", "/var/log/acdc.log"}},
		{name: "connect wait expiry", modify: func(s *config.Settings) { s.SharedServer.ConnectWait = 100 * time.Millisecond }, want: []string{"connect wait"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newBrowseTestServer(t, tt.reply)
			settings := browseTestSettings(t)
			if tt.modify != nil {
				tt.modify(settings)
			}
			params := server.params(t, settings, &bytes.Buffer{}, &bytes.Buffer{})
			if tt.reply == nil {
				params.SpawnSharedServer = func(string) error { return nil }
			}
			var stdout bytes.Buffer
			params.Stdout = &stdout

			err := RunBrowse(context.Background(), params, nil, sessionProxyTestBinary)

			require.Error(t, err)
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
			assert.Empty(t, stdout.String())
		})
	}
}

func TestBrowseSharedServerArgs_KeepsTheChangedSettingsAndDropsIgnoredFlags(t *testing.T) {
	flags := pflag.NewFlagSet("browse", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Parse([]string{
		"-c", "/content", "--search-in-memory=false", "--shared-server-linger", "2s",
		"--ui-enabled", "--ui-max-results", "5", "--host", "h", "-p", "1",
		"--auth-type", "apikey", "-k", "a,b", "--auth-basic-username", "u", "--auth-basic-password", "pw",
	}))

	args := browseSharedServerArgs(flags)

	assert.ElementsMatch(t, []string{
		"--content-dir=/content", "--search-in-memory=false", "--shared-server-linger=2s", "--ui-max-results=5",
	}, args)
	reparsed := pflag.NewFlagSet("server", pflag.ContinueOnError)
	RegisterFlags(reparsed)
	require.NoError(t, reparsed.Parse(args))
}

func TestRunWithDeps_AStdioLaunchWithUIEnabledWarnsThatTheUIIsIgnored(t *testing.T) {
	settings := sessionProxyTestSettings(t, "stdio", config.ServerModeSession)
	settings.UI = config.UISettings{Enabled: true, MaxResults: 20}
	params := sessionProxyTestParams(t, settings, nil)
	var stderr bytes.Buffer
	params.Stderr = &stderr

	err := RunWithDeps(context.Background(), params, nil, sessionProxyTestBinary)

	require.ErrorIs(t, err, errSessionProxyTestCreate)
	assert.Contains(t, stderr.String(), "Web UI requires the sse transport; ui.enabled is ignored")
}

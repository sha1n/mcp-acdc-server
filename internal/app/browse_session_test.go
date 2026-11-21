//go:build unix

package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const browseSessionTestWait = 5 * time.Second

var errBrowseSessionTestCreate = errors.New("index build failed")

func browseSessionTestSettings(t *testing.T) *config.Settings {
	t.Helper()
	settings := browseTestSettings(t)
	settings.ServerMode = config.ServerModeSession
	return settings
}

// browseSessionTestParams has no shared-server machinery: a foreground
// browse must not need an endpoint directory or a spawn.
func browseSessionTestParams(t *testing.T, settings *config.Settings, stdout io.Writer, cleanups *int) RunParams {
	t.Helper()
	return RunParams{
		Stdout:        stdout,
		Stderr:        io.Discard,
		LoadSettings:  func(*pflag.FlagSet) (*config.Settings, error) { return settings, nil },
		ValidSettings: config.ValidateSettings,
		CreateServer: func(context.Context, *slog.Logger, *config.Settings, string) (Components, error) {
			return Components{
				MCP:        mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil),
				SSEOptions: []SSEOption{WithWebUI(stubUIBackend{}, stubUIDocuments{})},
				Cleanup:    func() { *cleanups++ },
			}, nil
		},
	}
}

type browseSessionRun struct {
	cancel context.CancelFunc
	done   chan error
	stdout *lockedBuffer
}

func startBrowseSession(t *testing.T, params RunParams, args ...string) *browseSessionRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	run := &browseSessionRun{cancel: cancel, done: make(chan error, 1), stdout: params.Stdout.(*lockedBuffer)}
	go func() { run.done <- RunBrowse(ctx, params, browseTestFlags(t, args...), sessionProxyTestBinary) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-run.done:
			t.Logf("RunBrowse returned %v", err)
		case <-time.After(browseSessionTestWait):
			t.Error("RunBrowse did not return after its context was cancelled")
		}
	})
	return run
}

func (r *browseSessionRun) awaitURL(t *testing.T) string {
	t.Helper()
	return awaitBrowseURL(t, r.stdout, r.done)
}

// awaitBrowseURL returns the printed URL line, or fails when RunBrowse returns
// or prints nothing in time.
func awaitBrowseURL(t *testing.T, stdout *lockedBuffer, done chan error) string {
	t.Helper()
	deadline := time.After(browseSessionTestWait)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if out := stdout.String(); strings.HasSuffix(out, "\n") {
			return out
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("RunBrowse returned before printing a URL: %v", err)
		case <-deadline:
			t.Fatal("RunBrowse printed no URL in time")
		case <-tick.C:
		}
	}
}

func (r *browseSessionRun) stop(t *testing.T) error {
	t.Helper()
	r.cancel()
	select {
	case err := <-r.done:
		r.done <- err
		return err
	case <-time.After(browseSessionTestWait):
		t.Fatal("RunBrowse did not return after its context was cancelled")
		return nil
	}
}

func requireBrowseSessionUIServed(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: browseSessionTestWait}
	resp, err := client.Get(url) //nolint:gosec // the loopback URL printed by browse
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRunBrowse_SessionModeServesTheWebUIInTheForeground(t *testing.T) {
	cleanups := 0
	stdout := &lockedBuffer{}
	params := browseSessionTestParams(t, browseSessionTestSettings(t), stdout, &cleanups)
	opened := make(chan string, 1)
	params.OpenBrowser = func(url string) error {
		opened <- url
		return nil
	}

	run := startBrowseSession(t, params)
	printed := run.awaitURL(t)

	require.Regexp(t, `^http://127\.0\.0\.1:\d+/ui/\n$`, printed)
	url := strings.TrimSuffix(printed, "\n")
	requireBrowseSessionUIServed(t, url)
	select {
	case got := <-opened:
		assert.Equal(t, url, got)
	case <-time.After(browseSessionTestWait):
		t.Error("the browser was not opened on the URL")
	}
	require.NoError(t, run.stop(t))
	assert.Equal(t, 1, cleanups, "the built components are cleaned up")
	assert.Equal(t, printed, stdout.String(), "stdout holds the URL line only")
}

func TestRunBrowse_SessionModeNoOpenDoesNotOpenTheBrowser(t *testing.T) {
	cleanups := 0
	params := browseSessionTestParams(t, browseSessionTestSettings(t), &lockedBuffer{}, &cleanups)
	params.OpenBrowser = func(string) error {
		t.Error("the browser must not be opened with --no-open")
		return nil
	}

	run := startBrowseSession(t, params, "--no-open")
	requireBrowseSessionUIServed(t, strings.TrimSuffix(run.awaitURL(t), "\n"))

	require.NoError(t, run.stop(t))
}

func TestRunBrowse_SessionModeServesOnTheListenWebUIListener(t *testing.T) {
	cleanups := 0
	params := browseSessionTestParams(t, browseSessionTestSettings(t), &lockedBuffer{}, &cleanups)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	params.ListenWebUI = func() (net.Listener, error) { return listener, nil }

	run := startBrowseSession(t, params, "--no-open")
	printed := run.awaitURL(t)

	assert.Equal(t, "http://"+listener.Addr().String()+"/ui/\n", printed)
	requireBrowseSessionUIServed(t, strings.TrimSuffix(printed, "\n"))
	require.NoError(t, run.stop(t))
}

func TestRunBrowse_SessionModeNeverUsesTheSharedServerMachinery(t *testing.T) {
	cleanups := 0
	params := browseSessionTestParams(t, browseSessionTestSettings(t), &lockedBuffer{}, &cleanups)
	params.EndpointDir = func() (string, error) {
		t.Error("the endpoint directory must not be used")
		return "", errors.New("unexpected endpoint dir")
	}
	params.SpawnSharedServer = func(string) error {
		t.Error("a shared server must not be spawned")
		return errors.New("unexpected spawn")
	}

	run := startBrowseSession(t, params, "--no-open")
	requireBrowseSessionUIServed(t, strings.TrimSuffix(run.awaitURL(t), "\n"))

	require.NoError(t, run.stop(t))
}

func TestRunBrowse_SessionModeFails(t *testing.T) {
	tests := []struct {
		name         string
		modify       func(*RunParams)
		want         string
		wantCleanups int
	}{
		{
			name: "web UI bind error",
			modify: func(p *RunParams) {
				p.ListenWebUI = func() (net.Listener, error) { return nil, errors.New("address in use") }
			},
			want:         "address in use",
			wantCleanups: 1,
		},
		{
			name: "catalog build error",
			modify: func(p *RunParams) {
				p.CreateServer = func(context.Context, *slog.Logger, *config.Settings, string) (Components, error) {
					return Components{}, errBrowseSessionTestCreate
				}
			},
			want: errBrowseSessionTestCreate.Error(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanups := 0
			stdout := &lockedBuffer{}
			params := browseSessionTestParams(t, browseSessionTestSettings(t), stdout, &cleanups)
			tt.modify(&params)
			ctx, cancel := context.WithTimeout(context.Background(), browseSessionTestWait)
			defer cancel()

			err := RunBrowse(ctx, params, browseTestFlags(t, "--no-open"), sessionProxyTestBinary)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.NotErrorIs(t, ctx.Err(), context.DeadlineExceeded, "RunBrowse must fail, not serve until cancelled")
			assert.Empty(t, stdout.String())
			assert.Equal(t, tt.wantCleanups, cleanups)
		})
	}
}

package app

import (
	"bytes"
	"context"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"log/slog"
	"os"
	"regexp"
	"sync"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockedBuffer keeps a misrouted concurrent write an assertion failure
// instead of a data race.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunWithDeps_OrdinaryLaunchLogsToTheInjectedStderr(t *testing.T) {
	previous := slog.Default()
	stderr := &lockedBuffer{}
	params := DefaultRunParams()
	params.Stderr = stderr
	contentDir := writeLoggingDestinationContent(t, "Injected Guide")

	runOrdinaryStdioSession(t, params, loggingDestinationFlags(t, contentDir))

	logged := stderr.String()
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="Starting MCP Acdc server" version=9\.8\.7$`, logged)
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="Config: content_dir" value=`+regexp.QuoteMeta(contentDir)+`$`, logged)
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="Config: transport" value=stdio$`, logged)
	assert.Regexp(t, loadedResourceEntry("Injected Guide"), logged)
	assert.Same(t, previous, slog.Default(), "an ordinary launch must not install a process default")
}

func TestDefaultRunParams_StderrIsTheProcessStderr(t *testing.T) {
	assert.Same(t, os.Stderr, DefaultRunParams().Stderr)
}

func TestRunWithDeps_OrdinaryLaunchWithoutStderrStartsNothing(t *testing.T) {
	created := false
	connected := false
	params := DefaultRunParams()
	params.Stderr = nil
	params.CreateServer = func(context.Context, *slog.Logger, *config.Settings, string) (Components, error) {
		created = true
		return Components{}, nil
	}
	params.CustomIOTransport = &mockTransport{connectCalled: &connected}

	err := RunWithDeps(context.Background(), params, loggingDestinationFlags(t, t.TempDir()), identity.Binary{Version: "test"})

	require.ErrorContains(t, err, "runner requires a stderr writer")
	assert.False(t, created, "no server may be built without a log destination")
	assert.False(t, connected, "no transport may be started without a log destination")
}

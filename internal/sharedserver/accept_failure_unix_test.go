//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acceptFailureEnv selects the accept-failure child. Exhausting descriptors
// would break every test sharing the process, so it only ever happens in a
// child that runs one test.
const acceptFailureEnv = "ACDC_SHAREDSERVER_ACCEPT_FAILURE_CHILD"

const acceptFailureChildTimeout = 30 * time.Second

// acceptFailureFilesLimit is far above what the child holds before Build and
// keeps the fill loop short.
const acceptFailureFilesLimit = 256

func TestRun_AcceptFailureDuringBuildIsLoggedAsOneErrorEntry(t *testing.T) {
	requireAcceptFailureChildPasses(t, "TestRun_AcceptFailureDuringBuildChild")
}

func TestRun_AcceptFailureWhileServingIsLoggedAsOneErrorEntry(t *testing.T) {
	requireAcceptFailureChildPasses(t, "TestRun_AcceptFailureWhileServingChild")
}

func TestRun_AcceptFailureDuringBuildChild(t *testing.T) {
	skipUnlessAcceptFailureChild(t)
	runAcceptFailureChild(t, func(buildCtx context.Context, fillers *descriptorFillers) (*mcp.Server, func(), error) {
		defer fillers.close()
		<-buildCtx.Done()
		return nil, nil, buildCtx.Err()
	})
}

func TestRun_AcceptFailureWhileServingChild(t *testing.T) {
	skipUnlessAcceptFailureChild(t)
	runAcceptFailureChild(t, func(_ context.Context, fillers *descriptorFillers) (*mcp.Server, func(), error) {
		return mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil), fillers.close, nil
	})
}

func requireAcceptFailureChildPasses(t *testing.T, child string) {
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, "-test.run=^"+child+"$", "-test.v", "-test.count=1", "-test.timeout="+acceptFailureChildTimeout.String())
	cmd.Env = append(os.Environ(), acceptFailureEnv+"="+child)

	out, err := cmd.CombinedOutput()

	require.NoError(t, err, "the child must pass:\n%s", out)
	assert.Contains(t, string(out), "--- PASS: "+child, "the child must run, not skip:\n%s", out)
	t.Log(acceptFailureSchedulePattern.FindString(string(out)))
}

var acceptFailureSchedulePattern = regexp.MustCompile(`schedule: .*`)

func skipUnlessAcceptFailureChild(t *testing.T) {
	if os.Getenv(acceptFailureEnv) != t.Name() {
		t.Skip("runs only as the child of its accept failure test")
	}
}

type descriptorFillers struct {
	files []*os.File
}

func (f *descriptorFillers) close() {
	for _, file := range f.files {
		_ = file.Close()
	}
	f.files = nil
}

// fillDescriptors opens files until the process hits its descriptor limit, so
// that the next accept fails with EMFILE.
func fillDescriptors(t *testing.T) *descriptorFillers {
	fillers := &descriptorFillers{}
	for {
		file, err := os.Open(os.DevNull)
		if err != nil {
			if !errors.Is(err, syscall.EMFILE) {
				fillers.close()
				t.Errorf("filling descriptors: %v", err)
			}
			return fillers
		}
		fillers.files = append(fillers.files, file)
	}
}

func lowerSoftFilesLimit(t *testing.T) {
	var limit syscall.Rlimit
	require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit))
	if limit.Cur > acceptFailureFilesLimit {
		limit.Cur = acceptFailureFilesLimit
	}
	require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit))
}

type acceptFailureBuild func(buildCtx context.Context, fillers *descriptorFillers) (*mcp.Server, func(), error)

// runAcceptFailureChild exhausts descriptors inside Build and then connects a
// client socket allocated before the limit was reached. The accept loop runs
// concurrently, so two schedules are legitimate. If its accept is already
// pending, the connect wakes it and it fails with EMFILE. On Linux, accept4
// reserves the descriptor before it looks at the backlog, so an accept that
// starts after the fill fails with EMFILE on an empty backlog and closes the
// listener first; the connect is then refused. Neither schedule can be forced
// without a production seam: an accept parked before the fill is woken only by
// a connection, so a child that never connects can hang even on Linux. The
// outcome must be the same for both. Only
// a linger exit unlinks the socket, so an accept failure leaves a stale,
// undialable one.
func runAcceptFailureChild(t *testing.T, build acceptFailureBuild) {
	dir := shortTempDir(t)
	logDir := t.TempDir()
	lowerSoftFilesLimit(t)
	client, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	require.NoError(t, err)
	defer func() { _ = syscall.Close(client) }()
	socketPath := filepath.Join(dir, testIdentity+".sock")

	err = runOnce(t, context.Background(), Config{
		Identity:        testIdentity,
		CurrentIdentity: currentIsTestIdentity,
		EndpointDir:     dir,
		LogDir:          logDir,
		Linger:          time.Hour,
		Build: func(buildCtx context.Context, _ *slog.Logger) (*mcp.Server, func(), error) {
			fillers := fillDescriptors(t)
			connectToFailingAcceptLoop(t, client, socketPath, fillers)
			return build(buildCtx, fillers)
		},
	})

	require.ErrorIs(t, err, syscall.EMFILE)
	assert.ErrorContains(t, err, "failed to accept shared server connection")
	_, dialErr := net.Dial("unix", socketPath)
	assert.ErrorIs(t, dialErr, syscall.ECONNREFUSED, "the socket left behind must not be served")
	assertLockReleased(t, dir)
	requireOneRunFailureEntry(t, logDir, err)
}

func connectToFailingAcceptLoop(t *testing.T, client int, socketPath string, fillers *descriptorFillers) {
	err := syscall.Connect(client, &syscall.SockaddrUnix{Name: socketPath})
	switch {
	case err == nil:
		t.Log("schedule: the connect woke a pending accept")
	case errors.Is(err, syscall.ECONNREFUSED):
		t.Log("schedule: the accept failed before the connect")
	default:
		fillers.close()
		t.Errorf("connecting: %v", err)
	}
}

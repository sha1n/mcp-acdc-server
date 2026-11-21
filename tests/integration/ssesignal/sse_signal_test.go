//go:build unix

package ssesignal

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
)

const (
	shutdownGrace = app.DefaultShutdownGrace
	// promptExitBound separates "did not wait out the grace" from "waited it
	// out": a server with nothing in flight must exit below it. The margin
	// below the grace absorbs subprocess scheduling on a loaded host while
	// still failing any exit that waited for the grace.
	promptExitBound = shutdownGrace - time.Second
	// exitWatchdog only catches a hung process; it is generous so subprocess
	// scheduling on a loaded CI host cannot fail a correct shutdown.
	exitWatchdog     = shutdownGrace + 15*time.Second
	readinessTimeout = 15 * time.Second
	sampleContentDir = "../../../examples/sample-content"
	ignoreSIGINTExec = `trap "" INT; exec "$0" "$@"`
	shutdownStartedM = "shutting down"
	shutdownStoppedM = "stopped"
	// stderrPollInterval is the tightest practical polling of the log, so a
	// signal sent on a logged event lands as close to it as possible.
	stderrPollInterval = time.Millisecond
)

var acdcBinary string

func TestMain(m *testing.M) {
	os.Exit(runWithBuiltBinary(m))
}

func runWithBuiltBinary(m *testing.M) int {
	dir, err := os.MkdirTemp("", "acdc-ssesignal-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssesignal: failed to create the build dir: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	acdcBinary, err = testkit.BuildBinary(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssesignal: failed to build acdc-mcp: %v\n", err)
		return 1
	}
	return m.Run()
}

type launchOptions struct {
	ignoreSIGINT bool
	tmpDir       string
	extraArgs    []string
	// configure, if set, adjusts the command before it starts.
	configure func(*exec.Cmd)
}

type sseProcess struct {
	cmd     *exec.Cmd
	addr    string
	baseURL string
	stderr  *syncBuffer
	exited  chan struct{}
	waitErr error
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// startSSE launches the binary on an ephemeral port in an isolated
// environment and returns once /health answers.
func startSSE(t *testing.T, opts launchOptions) *sseProcess {
	t.Helper()
	work := t.TempDir()
	content := filepath.Join(work, "content")
	if err := os.CopyFS(content, os.DirFS(sampleContentDir)); err != nil {
		t.Fatalf("failed to copy the sample content: %v", err)
	}
	home := filepath.Join(work, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("failed to create HOME: %v", err)
	}
	tmpDir := opts.tmpDir
	if tmpDir == "" {
		tmpDir = filepath.Join(work, "tmp")
		if err := os.MkdirAll(tmpDir, 0o700); err != nil {
			t.Fatalf("failed to create TMPDIR: %v", err)
		}
	}

	port := testkit.MustGetFreePort(t)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	args := append([]string{
		"--content-dir", content,
		"--transport", "sse",
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
	}, opts.extraArgs...)

	var cmd *exec.Cmd
	if opts.ignoreSIGINT {
		// An ignored disposition survives exec, so the binary starts with
		// SIGINT ignored, as a background job of a non-interactive shell does.
		cmd = exec.Command("sh", append([]string{"-c", ignoreSIGINTExec, acdcBinary}, args...)...)
	} else {
		cmd = exec.Command(acdcBinary, args...)
	}
	cmd.Dir = work
	cmd.Env = append(isolatedEnviron(os.Environ()), "HOME="+home, "TMPDIR="+tmpDir)
	stderr := &syncBuffer{}
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	if opts.configure != nil {
		opts.configure(cmd)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start acdc-mcp: %v", err)
	}

	p := &sseProcess{cmd: cmd, addr: addr, baseURL: "http://" + addr, stderr: stderr, exited: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.exited)
	}()
	t.Cleanup(func() {
		select {
		case <-p.exited:
			return
		default:
		}
		_ = cmd.Process.Kill()
		select {
		case <-p.exited:
		case <-time.After(10 * time.Second):
			t.Errorf("acdc-mcp (pid %d) was not reaped after SIGKILL", cmd.Process.Pid)
		}
	})

	p.awaitHealthy(t)
	return p
}

func isolatedEnviron(environ []string) []string {
	var kept []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if name == "HOME" || name == "TMPDIR" || strings.HasPrefix(name, "XDG_") || strings.HasPrefix(name, "ACDC_MCP_") {
			continue
		}
		kept = append(kept, kv)
	}
	return kept
}

func (p *sseProcess) awaitHealthy(t *testing.T) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(readinessTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-p.exited:
			t.Fatalf("acdc-mcp exited before it was ready: %v\nstderr:\n%s", p.waitErr, p.stderr)
		default:
		}
		resp, err := client.Get(p.baseURL + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("acdc-mcp was not healthy within %v\nstderr:\n%s", readinessTimeout, p.stderr)
}

func (p *sseProcess) signal(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := p.cmd.Process.Signal(sig); err != nil {
		t.Fatalf("failed to send %v: %v", sig, err)
	}
}

// awaitExit waits for the process to exit and returns how long that took
// since start; it fails the test if the process is still running at timeout.
func (p *sseProcess) awaitExit(t *testing.T, start time.Time, timeout time.Duration) time.Duration {
	t.Helper()
	select {
	case <-p.exited:
		return time.Since(start)
	case <-time.After(timeout):
		t.Fatalf("acdc-mcp did not exit within %v of the signal\nstderr:\n%s", timeout, p.stderr)
		return 0
	}
}

// awaitPromptExit waits for the process to exit and fails the test if the
// exit took long enough to have waited out the shutdown grace.
func (p *sseProcess) awaitPromptExit(t *testing.T, start time.Time) {
	t.Helper()
	if elapsed := p.awaitExit(t, start, exitWatchdog); elapsed >= promptExitBound {
		t.Errorf("exit took %v, want < %v: the shutdown waited out the %v grace\nstderr:\n%s", elapsed, promptExitBound, shutdownGrace, p.stderr)
	}
}

func (p *sseProcess) requireExitCodeZero(t *testing.T) {
	t.Helper()
	state := p.cmd.ProcessState
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		t.Fatalf("acdc-mcp was killed by signal %v, want exit code 0\nstderr:\n%s", ws.Signal(), p.stderr)
	}
	if code := state.ExitCode(); code != 0 {
		t.Fatalf("acdc-mcp exited with code %d, want 0\nstderr:\n%s", code, p.stderr)
	}
}

// awaitStderr waits until the stderr written after offset contains want,
// case-insensitively, polling every stderrPollInterval.
func (p *sseProcess) awaitStderr(t *testing.T, offset int, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if strings.Contains(strings.ToLower(p.stderr.String()[offset:]), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stderr did not log %q within %v of the signal\nstderr:\n%s", want, timeout, p.stderr)
		}
		select {
		case <-p.exited:
			if !strings.Contains(strings.ToLower(p.stderr.String()[offset:]), want) {
				t.Fatalf("acdc-mcp exited (%v) without logging %q\nstderr:\n%s", p.waitErr, want, p.stderr)
			}
			return
		case <-time.After(stderrPollInterval):
		}
	}
}

// openInFlightRequest holds a real handler open until the server force-closes
// its connection. It posts to an /sse session endpoint with
// "Expect: 100-continue" and never sends the body: the server answers
// 100 Continue only once the handler reads the body, so reading that answer
// proves the request was accepted and its handler is running, blocked in a
// read that ignores cancellation.
func openInFlightRequest(t *testing.T, p *sseProcess) {
	t.Helper()
	endpoint := openSessionEndpoint(t, p)
	conn, err := net.DialTimeout("tcp", p.addr, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to dial %s: %v", p.addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	request := "POST " + endpoint + " HTTP/1.1\r\nHost: " + p.addr +
		"\r\nContent-Type: application/json\r\nContent-Length: 2\r\nExpect: 100-continue\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("failed to write the request headers: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(readinessTimeout)); err != nil {
		t.Fatalf("failed to set a read deadline: %v", err)
	}
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("the handler never read the request body: %v\nstderr:\n%s", err, p.stderr)
	}
	if !strings.HasPrefix(status, "HTTP/1.1 100 ") {
		t.Fatalf("got %q, want a 100 Continue from a handler reading the body", strings.TrimSpace(status))
	}
}

// openSessionEndpoint opens an /sse stream for the rest of the test and
// returns the request URI of its session's message endpoint.
func openSessionEndpoint(t *testing.T, p *sseProcess) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// The stream outlives this function, so only the request and the
	// endpoint read are bounded: the timer is stopped once the event arrives.
	timedOut := time.AfterFunc(readinessTimeout, cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/sse", nil)
	if err != nil {
		t.Fatalf("failed to build the /sse request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /sse failed within %v: %v\nstderr:\n%s", readinessTimeout, err, p.stderr)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sse status = %d, want 200", resp.StatusCode)
	}
	data := awaitEndpointEvent(t, bufio.NewReader(resp.Body))
	if !timedOut.Stop() {
		t.Fatalf("the /sse endpoint event did not arrive within %v\nstderr:\n%s", readinessTimeout, p.stderr)
	}
	endpoint, err := url.Parse(data)
	if err != nil {
		t.Fatalf("the endpoint event data %q is not a URL: %v", data, err)
	}
	return endpoint.RequestURI()
}

func TestSSESignal_SIGTERMIdleExitsZeroPromptly(t *testing.T) {
	p := startSSE(t, launchOptions{})
	offset := p.stderr.Len()

	start := time.Now()
	p.signal(t, syscall.SIGTERM)
	p.awaitPromptExit(t, start)

	p.requireExitCodeZero(t)
	logged := strings.ToLower(p.stderr.String()[offset:])
	for _, want := range []string{shutdownStartedM, shutdownStoppedM} {
		if !strings.Contains(logged, want) {
			t.Errorf("stderr after the signal does not contain %q\nstderr:\n%s", want, p.stderr)
		}
	}
}

func TestSSESignal_SIGTERMWithOpenSSEStreamEndsStreamAndExitsZero(t *testing.T) {
	p := startSSE(t, launchOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/sse", nil)
	if err != nil {
		t.Fatalf("failed to build the /sse request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /sse failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sse status = %d, want 200", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)
	awaitEndpointEvent(t, reader)

	streamEnded := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, reader)
		close(streamEnded)
	}()

	start := time.Now()
	p.signal(t, syscall.SIGTERM)
	p.awaitPromptExit(t, start)
	p.requireExitCodeZero(t)

	select {
	case <-streamEnded:
	case <-time.After(exitWatchdog):
		t.Fatalf("the /sse stream did not end within %v of the exit", exitWatchdog)
	}
}

// awaitEndpointEvent reads up to the endpoint event and returns its data.
func awaitEndpointEvent(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("the /sse stream ended before the endpoint event: %v", err)
		}
		if strings.TrimSpace(line) == "event: endpoint" {
			data, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("the /sse stream ended before the endpoint data: %v", err)
			}
			return strings.TrimSpace(strings.TrimPrefix(data, "data:"))
		}
	}
}

func TestSSESignal_SIGINTExitsZero(t *testing.T) {
	tests := []struct {
		name         string
		ignoreSIGINT bool
	}{
		{name: "default disposition", ignoreSIGINT: false},
		{name: "started with SIGINT ignored", ignoreSIGINT: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := startSSE(t, launchOptions{ignoreSIGINT: tc.ignoreSIGINT})

			start := time.Now()
			p.signal(t, syscall.SIGINT)
			p.awaitPromptExit(t, start)
			p.requireExitCodeZero(t)
		})
	}
}

func TestSSESignal_SIGTERMRemovesOnDiskIndex(t *testing.T) {
	tmpDir := t.TempDir()
	p := startSSE(t, launchOptions{tmpDir: tmpDir, extraArgs: []string{"--search-in-memory=false"}})

	pattern := filepath.Join(tmpDir, "acdc_search_*")
	if dirs, _ := filepath.Glob(pattern); len(dirs) == 0 {
		t.Fatalf("no %s directory while the server runs", pattern)
	}

	start := time.Now()
	p.signal(t, syscall.SIGTERM)
	p.awaitPromptExit(t, start)

	if dirs, _ := filepath.Glob(pattern); len(dirs) != 0 {
		t.Errorf("index directories left behind after the exit: %v", dirs)
	}
	p.requireExitCodeZero(t)
}

func TestSSESignal_InFlightRequestHoldsExitForTheGraceThenExitsZeroAndCleansUp(t *testing.T) {
	tmpDir := t.TempDir()
	p := startSSE(t, launchOptions{tmpDir: tmpDir, extraArgs: []string{"--search-in-memory=false"}})
	pattern := filepath.Join(tmpDir, "acdc_search_*")
	if dirs, _ := filepath.Glob(pattern); len(dirs) == 0 {
		t.Fatalf("no %s directory while the server runs", pattern)
	}
	openInFlightRequest(t, p)

	start := time.Now()
	p.signal(t, syscall.SIGTERM)
	elapsed := p.awaitExit(t, start, exitWatchdog)

	p.requireExitCodeZero(t)
	// Half the grace is far above a prompt exit yet far below the grace, so
	// it proves the handler was waited on without timing the force-close.
	if minimum := shutdownGrace / 2; elapsed < minimum {
		t.Errorf("exit took %v, want at least %v: the in-flight request was not given the grace", elapsed, minimum)
	}
	if dirs, _ := filepath.Glob(pattern); len(dirs) != 0 {
		t.Errorf("index directories left behind after the exit: %v", dirs)
	}
}

func (p *sseProcess) requireExitCode(t *testing.T, want int) {
	t.Helper()
	state := p.cmd.ProcessState
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		t.Fatalf("acdc-mcp was killed by signal %v (default disposition), want exit code %d\nstderr:\n%s", ws.Signal(), want, p.stderr)
	}
	if code := state.ExitCode(); code != want {
		t.Fatalf("acdc-mcp exited with code %d, want %d\nstderr:\n%s", code, want, p.stderr)
	}
}

func TestSSESignal_SecondSignalDuringShutdownTerminatesImmediately(t *testing.T) {
	tests := []struct {
		name         string
		sig          syscall.Signal
		ignoreSIGINT bool
	}{
		{name: "SIGTERM", sig: syscall.SIGTERM},
		{name: "SIGINT", sig: syscall.SIGINT},
		{name: "SIGINT started ignored", sig: syscall.SIGINT, ignoreSIGINT: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := startSSE(t, launchOptions{ignoreSIGINT: tc.ignoreSIGINT})
			requireSecondSignalExits(t, p, tc.sig)
		})
	}
}

// requireSecondSignalExits holds the graceful shutdown open with a stuck
// request, sends sig twice and requires the second one to end the process
// promptly with exit code 128+signo.
func requireSecondSignalExits(t *testing.T, p *sseProcess, sig syscall.Signal) {
	t.Helper()
	openInFlightRequest(t, p)
	offset := p.stderr.Len()

	firstSent := time.Now()
	p.signal(t, sig)
	// The second signal must land during the graceful shutdown, not before
	// the handler took over, or it would prove nothing. It is sent at once
	// after the shutdown-start log, so a window in which the handler drops
	// it fails the test.
	p.awaitStderr(t, offset, shutdownStartedM, exitWatchdog)
	start := time.Now()
	err := p.cmd.Process.Signal(sig)
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("failed to send the second %v: %v", sig, err)
	}
	// The stuck request holds the drain for the whole grace from the shutdown
	// start, which is after firstSent, so a clean exit with the second
	// signal sent later than that is a stalled host, not a dropped signal.
	drainMayHaveElapsed := errors.Is(err, os.ErrProcessDone) || start.Sub(firstSent) >= shutdownGrace
	elapsed := p.awaitExit(t, start, exitWatchdog)
	if drainMayHaveElapsed && p.cmd.ProcessState.ExitCode() == 0 {
		t.Fatalf("acdc-mcp exited 0: the %v drain elapsed before the second %v was delivered (sent %v after the first); the host stalled the test for longer than the grace\nstderr:\n%s",
			shutdownGrace, sig, start.Sub(firstSent), p.stderr)
	}
	p.requireExitCode(t, 128+int(sig))
	if elapsed >= promptExitBound {
		t.Errorf("exit took %v after the second %v, want < %v\nstderr:\n%s", elapsed, sig, promptExitBound, p.stderr)
	}
}

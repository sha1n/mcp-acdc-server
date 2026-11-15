//go:build unix

package testkit

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
)

// LeakGuardGrace is generous on purpose: the guard detects a shared server
// that never exits, it does not measure the linger.
const LeakGuardGrace = 30 * time.Second

// BuildBinary builds the acdc-mcp command into dir with a plain go build, so
// the binary is unstamped and carries neither race nor coverage
// instrumentation.
func BuildBinary(dir string) (string, error) {
	moduleDir, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return "", fmt.Errorf("failed to locate the module: %w", err)
	}
	binary := filepath.Join(dir, "acdc-mcp")
	build := exec.Command("go", "build", "-o", binary, "./cmd/acdc-mcp")
	build.Dir = strings.TrimSpace(string(moduleDir))
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build failed: %w\n%s", err, out)
	}
	return binary, nil
}

// BinaryEnv is one isolated user environment for real acdc-mcp processes:
// its own HOME, XDG_RUNTIME_DIR, XDG_STATE_HOME and TMPDIR under a short
// directory directly under /tmp, so socket paths stay inside the platform
// limit and no real user directory is touched.
type BinaryEnv struct {
	Binary string
	Root   string
	env    []string
}

// NewBinaryEnv creates the environment and registers its leak guard. The
// guard is registered after the directory removal, so it runs before it and
// still finds the shared server logs.
func NewBinaryEnv(t testing.TB, binary string) *BinaryEnv {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "acdc-")
	if err != nil {
		t.Fatalf("failed to create the isolated root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	e := &BinaryEnv{Binary: binary, Root: root}
	for _, dir := range []string{e.home(), e.runtimeDir(), e.stateDir(), e.TempDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("failed to create %s: %v", dir, err)
		}
	}
	e.env = append(isolatedEnviron(os.Environ()),
		"HOME="+e.home(),
		"XDG_RUNTIME_DIR="+e.runtimeDir(),
		"XDG_STATE_HOME="+e.stateDir(),
		"TMPDIR="+e.TempDir(),
	)
	t.Cleanup(func() { CheckSharedServerLeaks(t, e.LogDir(), e.Root, LeakGuardGrace) })
	return e
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

func (e *BinaryEnv) home() string       { return filepath.Join(e.Root, "h") }
func (e *BinaryEnv) runtimeDir() string { return filepath.Join(e.Root, "r") }
func (e *BinaryEnv) stateDir() string   { return filepath.Join(e.Root, "s") }

// TempDir is the system temp dir of every process of this environment.
func (e *BinaryEnv) TempDir() string { return filepath.Join(e.Root, "t") }

// EndpointDir is where a process of this environment keeps sockets and locks.
func (e *BinaryEnv) EndpointDir() string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(e.home(), "Library", "Caches", "acdc-mcp")
	}
	return filepath.Join(e.runtimeDir(), "acdc-mcp")
}

// LogDir is where a shared server of this environment writes its log.
func (e *BinaryEnv) LogDir() string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(e.home(), "Library", "Logs", "acdc-mcp")
	}
	return filepath.Join(e.stateDir(), "acdc-mcp")
}

// BinaryLaunch is one acdc-mcp process with its host stdio as pipes.
type BinaryLaunch struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	lines    chan string
	stderr   *lockedLines
	done     chan struct{}
	exitCode int
	waitErr  error
}

// Start launches the binary with args. The process is killed at the end of
// the test if it is still running.
func (e *BinaryEnv) Start(t testing.TB, args ...string) *BinaryLaunch {
	t.Helper()
	cmd := exec.Command(e.Binary, args...)
	cmd.Env = e.env
	cmd.Dir = e.Root
	l := &BinaryLaunch{cmd: cmd, lines: make(chan string, 64), stderr: &lockedLines{}, done: make(chan struct{})}
	cmd.Stderr = l.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	l.stdin = stdin
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start %s: %v", e.Binary, err)
	}
	go l.pump(stdout)
	t.Cleanup(l.kill)
	return l
}

func (l *BinaryLaunch) pump(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		l.lines <- scanner.Text()
	}
	close(l.lines)
	err := l.cmd.Wait()
	l.exitCode = l.cmd.ProcessState.ExitCode()
	l.waitErr = err
	close(l.done)
}

func (l *BinaryLaunch) kill() {
	select {
	case <-l.done:
		return
	default:
	}
	_ = l.cmd.Process.Kill()
	_ = l.stdin.Close()
	for range l.lines {
	}
	<-l.done
}

// Send writes one line to the process's stdin.
func (l *BinaryLaunch) Send(line string) error {
	_, err := io.WriteString(l.stdin, line+"\n")
	return err
}

// CloseStdin ends the host input, which ends a session.
func (l *BinaryLaunch) CloseStdin() error {
	return l.stdin.Close()
}

// ReadLine returns the next stdout line, or an error at the deadline or when
// stdout ends.
func (l *BinaryLaunch) ReadLine(deadline time.Time) (string, error) {
	select {
	case line, ok := <-l.lines:
		if !ok {
			return "", errors.New("stdout ended")
		}
		return line, nil
	case <-time.After(time.Until(deadline)):
		return "", errors.New("no stdout line before the deadline")
	}
}

// Wait returns the exit code once the process has exited.
func (l *BinaryLaunch) Wait(deadline time.Time) (int, error) {
	select {
	case <-l.done:
		return l.exitCode, nil
	case <-time.After(time.Until(deadline)):
		return -1, errors.New("the process did not exit before the deadline")
	}
}

// Stderr returns what the process has written to stderr so far.
func (l *BinaryLaunch) Stderr() string {
	return l.stderr.String()
}

// AwaitStderrRecord polls stderr for the first slog record matching match.
func (l *BinaryLaunch) AwaitStderrRecord(match slogtext.Record, deadline time.Time) (slogtext.Record, error) {
	for {
		if found := matchingStderrRecords(l.Stderr(), match); len(found) > 0 {
			return found[0], nil
		}
		select {
		case <-l.done:
			if found := matchingStderrRecords(l.Stderr(), match); len(found) > 0 {
				return found[0], nil
			}
			return nil, fmt.Errorf("the process exited without a stderr record %v; stderr:\n%s", match, l.Stderr())
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no stderr record %v before the deadline; stderr:\n%s", match, l.Stderr())
		}
	}
}

func matchingStderrRecords(stderr string, match slogtext.Record) []slogtext.Record {
	var records []slogtext.Record
	for _, line := range strings.Split(stderr, "\n") {
		if !strings.HasPrefix(line, "time=") {
			continue
		}
		parsed, err := slogtext.Parse([]byte(line))
		if err != nil {
			continue
		}
		records = append(records, slogtext.Filter(parsed, match)...)
	}
	return records
}

// AwaitAttach returns the session id and log path of the launch's attach line.
func (l *BinaryLaunch) AwaitAttach(deadline time.Time) (session, logPath string, err error) {
	record, err := l.AwaitStderrRecord(slogtext.Record{"msg": "Attached to the shared server"}, deadline)
	if err != nil {
		return "", "", err
	}
	return record["session"], record["log"], nil
}

type lockedLines struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedLines) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedLines) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

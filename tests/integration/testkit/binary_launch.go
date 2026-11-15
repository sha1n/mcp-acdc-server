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

	"github.com/sha1n/mcp-acdc-server/internal/config"
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

	mu              sync.Mutex
	launched        bool
	maxLaunchLinger time.Duration
}

// NewBinaryEnv creates the environment and registers its leak guard. The
// guard is registered after the directory removal, so it runs before it and
// still finds the shared server logs and locks, and before every launch,
// whose kill therefore runs before it.
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
	t.Cleanup(func() { e.CheckSharedServerLeaks(t) })
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

func (e *BinaryEnv) recordLaunch(args []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if linger := launchLinger(args); !e.launched || linger > e.maxLaunchLinger {
		e.maxLaunchLinger = linger
	}
	e.launched = true
}

// maxLinger is the largest linger a launch of this environment started its
// shared server with.
func (e *BinaryEnv) maxLinger() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.launched {
		return config.DefaultSharedServerLinger
	}
	return e.maxLaunchLinger
}

func launchLinger(args []string) time.Duration {
	linger := config.DefaultSharedServerLinger
	for i, arg := range args {
		value, inline := strings.CutPrefix(arg, "--shared-server-linger=")
		if !inline && arg == "--shared-server-linger" && i+1 < len(args) {
			value, inline = args[i+1], true
		}
		if parsed, err := time.ParseDuration(value); inline && err == nil {
			linger = parsed
		}
	}
	return linger
}

// BinaryLaunch is one acdc-mcp process with its host stdio as pipes.
type BinaryLaunch struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	lines    chan string
	stderr   *lockedLines
	output   *lockedLines
	done     chan struct{}
	exitCode int
	waitErr  error
}

// Start launches the binary with args. The process is killed at the end of
// the test if it is still running.
func (e *BinaryEnv) Start(t testing.TB, args ...string) *BinaryLaunch {
	t.Helper()
	l := e.newLaunch(args)
	l.cmd.Stderr = l.stderr
	stdout, err := l.cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	l.start(t, stdout, nil, l.relayStdoutLine)
	return l
}

// StartInterleaved launches the binary as Start does, but with stdout and
// stderr on one OS pipe, so Output keeps the order of the writes across
// both streams. The streams are told apart by content: ReadLine returns the
// JSON-RPC lines, Stderr the others.
func (e *BinaryEnv) StartInterleaved(t testing.TB, args ...string) *BinaryLaunch {
	t.Helper()
	l := e.newLaunch(args)
	output, outputWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("output pipe: %v", err)
	}
	l.cmd.Stdout, l.cmd.Stderr = outputWriter, outputWriter
	l.start(t, output, outputWriter, l.classifyOutputLine)
	return l
}

func (e *BinaryEnv) newLaunch(args []string) *BinaryLaunch {
	e.recordLaunch(args)
	cmd := exec.Command(e.Binary, args...)
	cmd.Env = e.env
	cmd.Dir = e.Root
	return &BinaryLaunch{cmd: cmd, lines: make(chan string, 64), stderr: &lockedLines{}, output: &lockedLines{}, done: make(chan struct{})}
}

// start closes childEnd, the parent's copy of a pipe end the child writes, so
// that the read end ends with the child.
func (l *BinaryLaunch) start(t testing.TB, stdout io.ReadCloser, childEnd io.Closer, onLine func(string)) {
	t.Helper()
	stdin, err := l.cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	l.stdin = stdin
	err = l.cmd.Start()
	if childEnd != nil {
		_ = childEnd.Close()
	}
	if err != nil {
		_ = stdout.Close()
		t.Fatalf("failed to start %s: %v", l.cmd.Path, err)
	}
	go l.pump(stdout, onLine)
	t.Cleanup(l.kill)
}

func (l *BinaryLaunch) pump(stdout io.ReadCloser, onLine func(string)) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		onLine(scanner.Text())
	}
	close(l.lines)
	_ = stdout.Close()
	err := l.cmd.Wait()
	l.exitCode = l.cmd.ProcessState.ExitCode()
	l.waitErr = err
	close(l.done)
}

func (l *BinaryLaunch) relayStdoutLine(line string) {
	l.lines <- line
}

func (l *BinaryLaunch) classifyOutputLine(line string) {
	_, _ = l.output.Write([]byte(line + "\n"))
	if strings.HasPrefix(line, "{") {
		l.lines <- line
		return
	}
	_, _ = l.stderr.Write([]byte(line + "\n"))
}

// Output returns, in write order, what a StartInterleaved process has
// written to stdout and stderr so far.
func (l *BinaryLaunch) Output() string {
	return l.output.String()
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

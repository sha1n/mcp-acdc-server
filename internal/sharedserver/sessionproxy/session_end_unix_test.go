//go:build unix

package sessionproxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	firstIdentity  = "0123456789abcdef0123456789abcdef"
	secondIdentity = "fedcba9876543210fedcba9876543210"
)

func sessionEndTestDir(t *testing.T) string {
	t.Helper()
	parent, err := os.MkdirTemp("/tmp", "acdc-end-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	return filepath.Join(parent, "ep")
}

// identityListener accepts on the socket of one identity in an endpoint
// directory and hands every accepted connection to the test.
type identityListener struct {
	t        *testing.T
	listener net.Listener
	accepted chan net.Conn
}

func listenFor(t *testing.T, dir, identity string) *identityListener {
	t.Helper()
	ep, err := endpoint.OpenEndpoint(dir)
	require.NoError(t, err)
	socketPath, err := ep.SocketPath(identity)
	require.NoError(t, err)
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	l := &identityListener{t: t, listener: listener, accepted: make(chan net.Conn, 4)}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
			l.accepted <- conn
		}
	}()
	return l
}

func (l *identityListener) nextConn() net.Conn {
	l.t.Helper()
	select {
	case conn := <-l.accepted:
		return conn
	case <-time.After(supervisionTestDeadline):
		l.t.Fatal("nothing connected to the listener")
		return nil
	}
}

// endedSession runs a session over a host input the test owns, so a callback
// can end the session and know host EOF was observed before it returns.
type endedSession struct {
	t         *testing.T
	ctx       context.Context
	cancel    context.CancelFunc
	stdin     *io.PipeWriter
	input     *hostInput
	fallbacks atomic.Int32
	warnings  atomic.Int32
}

type warningCounter struct{ count *atomic.Int32 }

func (w warningCounter) Enabled(context.Context, slog.Level) bool { return true }

func (w warningCounter) Handle(_ context.Context, record slog.Record) error {
	if record.Level >= slog.LevelWarn {
		w.count.Add(1)
	}
	return nil
}

func (w warningCounter) WithAttrs([]slog.Attr) slog.Handler { return w }

func (w warningCounter) WithGroup(string) slog.Handler { return w }

func newEndedSession(t *testing.T) *endedSession {
	t.Helper()
	stdinReader, stdinWriter := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	s := &endedSession{t: t, ctx: ctx, cancel: cancel, stdin: stdinWriter, input: readHostInput(stdinReader)}
	t.Cleanup(func() {
		cancel()
		_ = stdinWriter.Close()
		s.input.stop()
	})
	return s
}

func (s *endedSession) config(dir string) Config {
	return Config{
		EndpointDir: func() (string, error) { return dir, nil },
		Identity:    func() (string, error) { return firstIdentity, nil },
		ConnectWait: supervisionTestDeadline,
		Spawn:       func(string) error { return nil },
		Fallback: func(context.Context, io.ReadCloser) error {
			s.fallbacks.Add(1)
			return errors.New("unexpected fallback")
		},
		Logger: slog.New(warningCounter{count: &s.warnings}),
		Stdout: io.Discard,
	}
}

func (s *endedSession) endByCancellation() { s.cancel() }

func (s *endedSession) endByEOF() {
	_ = s.stdin.Close()
	select {
	case <-s.input.ended:
	case <-time.After(supervisionTestDeadline):
		s.t.Error("host EOF was never observed")
	}
}

// endAsync runs the unattached path of Run over the host input the test owns.
func (s *endedSession) endAsync(cfg Config) <-chan error {
	done := make(chan error, 1)
	go func() {
		att, err := establish(s.ctx, cfg, s.input.ended)
		if err == nil {
			_ = att.conn.Close()
			done <- errors.New("attached instead of ending")
			return
		}
		done <- endWithoutAttach(s.ctx, cfg, err, s.input)
	}()
	return done
}

func (s *endedSession) requireEndedWithoutFallback(done <-chan error) {
	s.t.Helper()
	select {
	case err := <-done:
		require.NoError(s.t, err)
	case <-time.After(supervisionTestDeadline):
		s.t.Fatal("the session did not end")
	}
	assert.Zero(s.t, s.fallbacks.Load(), "a session whose host ended never falls back")
	assert.Zero(s.t, s.warnings.Load(), "a session whose host ended never warns")
}

// onCall runs end and then fails on the given call of a callback, so the
// failure is returned only once the session has ended.
func onCall(n int32, end func(), pass func() (string, error), fail func() (string, error)) func() (string, error) {
	var calls atomic.Int32
	return func() (string, error) {
		if calls.Add(1) < n {
			return pass()
		}
		end()
		return fail()
	}
}

func dropFirstConnection(t *testing.T, dir string) {
	l := listenFor(t, dir, firstIdentity)
	go func() {
		conn := <-l.accepted
		_ = l.listener.Close()
		_ = conn.Close()
	}()
}

type sessionEndCase struct {
	name      string
	configure func(t *testing.T, dir string, end func(), cfg *Config)
}

func failingPhaseCases() []sessionEndCase {
	fixedDir := func(dir string) func() (string, error) { return func() (string, error) { return dir, nil } }
	identity := func() (string, error) { return firstIdentity, nil }
	failure := func() (string, error) { return "", errors.New("phase failed") }
	var cases []sessionEndCase
	for _, attempt := range []struct {
		name string
		call int32
	}{{"first attempt", 1}, {"re-entry", 2}} {
		reentry := attempt.call > 1
		prepare := func(t *testing.T, dir string) {
			if reentry {
				dropFirstConnection(t, dir)
			}
		}
		cases = append(cases,
			sessionEndCase{name: attempt.name + "/identity", configure: func(t *testing.T, dir string, end func(), cfg *Config) {
				prepare(t, dir)
				cfg.Identity = onCall(attempt.call, end, identity, failure)
			}},
			sessionEndCase{name: attempt.name + "/endpoint", configure: func(t *testing.T, dir string, end func(), cfg *Config) {
				prepare(t, dir)
				cfg.EndpointDir = onCall(attempt.call, end, fixedDir(dir), failure)
			}},
			sessionEndCase{name: attempt.name + "/endpoint cancellation error", configure: func(t *testing.T, dir string, end func(), cfg *Config) {
				prepare(t, dir)
				cfg.EndpointDir = onCall(attempt.call, end, fixedDir(dir), func() (string, error) { return "", context.Canceled })
			}},
			sessionEndCase{name: attempt.name + "/socket path", configure: func(t *testing.T, dir string, end func(), cfg *Config) {
				prepare(t, dir)
				cfg.EndpointDir = onCall(attempt.call, end, fixedDir(dir), fixedDir(filepath.Join(dir, strings.Repeat("d", 120))))
			}},
			sessionEndCase{name: attempt.name + "/spawn", configure: func(t *testing.T, dir string, end func(), cfg *Config) {
				prepare(t, dir)
				cfg.Spawn = func(string) error { end(); return errors.New("exec failed") }
			}},
			sessionEndCase{name: attempt.name + "/connect wait", configure: func(t *testing.T, dir string, end func(), cfg *Config) {
				prepare(t, dir)
				cfg.ConnectWait = 10 * time.Millisecond
				cfg.Spawn = func(string) error { end(); return nil }
			}},
		)
	}
	return append(cases, sessionEndCase{name: "second drop", configure: func(t *testing.T, dir string, end func(), _ *Config) {
		l := listenFor(t, dir, firstIdentity)
		go func() {
			_ = (<-l.accepted).Close()
			second := <-l.accepted
			end()
			_ = second.Close()
		}()
	}})
}

func TestSessionWithoutAttach_NeverFallsBackWhenTheHostEndedBeforeAPhaseFailed(t *testing.T) {
	for _, tt := range failingPhaseCases() {
		for _, way := range sessionEndWays {
			t.Run(tt.name+"/"+way, func(t *testing.T) {
				s := newEndedSession(t)
				end := s.end(way)
				dir := sessionEndTestDir(t)
				cfg := s.config(dir)
				tt.configure(t, dir, end, &cfg)

				s.requireEndedWithoutFallback(s.endAsync(cfg))
			})
		}
	}
}

func TestEstablish_DoesNotClassifyACancellationReportedByTheEndpointResolver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := Config{
		Identity:    func() (string, error) { return firstIdentity, nil },
		EndpointDir: func() (string, error) { cancel(); return "", context.Canceled },
		Logger:      slog.New(slog.DiscardHandler),
	}

	_, err := establish(ctx, cfg, make(chan struct{}))

	var failure *connectionSetupError
	require.False(t, errors.As(err, &failure), "cancellation classified as a discovery failure")
	require.ErrorIs(t, err, context.Canceled)
}

func TestEstablish_ReportsTheSessionEndInsteadOfAPhaseFailureThatFollowedIt(t *testing.T) {
	for _, tt := range failingPhaseCases() {
		for _, way := range sessionEndWays {
			t.Run(tt.name+"/"+way, func(t *testing.T) {
				s := newEndedSession(t)
				end := s.end(way)
				dir := sessionEndTestDir(t)
				cfg := s.config(dir)
				tt.configure(t, dir, end, &cfg)

				att, err := establish(s.ctx, cfg, s.input.ended)

				require.Nil(t, att)
				var failure *connectionSetupError
				require.False(t, errors.As(err, &failure), "a session end classified as a %v failure", err)
				if way == "stdin EOF" {
					require.ErrorIs(t, err, errSessionEnded)
				} else {
					require.ErrorIs(t, err, context.Canceled)
				}
			})
		}
	}
}

func (s *endedSession) end(way string) func() {
	if way == "stdin EOF" {
		return s.endByEOF
	}
	return s.endByCancellation
}

var sessionEndWays = []string{"cancellation", "stdin EOF"}

func TestEndWithoutAttach_NeverFallsBackOnceTheHostEndedAfterDiscoveryFailed(t *testing.T) {
	causes := []failureCause{causeIdentity, causeEndpoint, causeSocketPath, causeSpawn, causeConnectWait, causeDroppedTwice}
	for _, cause := range causes {
		for _, way := range sessionEndWays {
			t.Run(string(cause)+"/"+way, func(t *testing.T) {
				s := newEndedSession(t)
				cfg := s.config(sessionEndTestDir(t))
				failure := &connectionSetupError{cause: cause, err: errors.New("discovery failed")}
				s.end(way)()

				err := endWithoutAttach(s.ctx, cfg, failure, s.input)

				require.NoError(t, err)
				assert.Zero(t, s.fallbacks.Load(), "a session whose host ended never falls back")
				assert.Zero(t, s.warnings.Load(), "a session whose host ended never warns")
			})
		}
	}
}

// endingError ends the session while discovery formats the spawn failure, so
// the session ends after that failure was classified but before establish
// returns it.
type endingError struct {
	once sync.Once
	end  func()
}

func (e *endingError) Error() string {
	e.once.Do(e.end)
	return "exec failed"
}

func TestEstablish_ReportsASessionEndObservedWhileTheSpawnFailureWasClassified(t *testing.T) {
	for _, reentry := range []bool{false, true} {
		for _, way := range sessionEndWays {
			name := "first attempt/" + way
			if reentry {
				name = "re-entry/" + way
			}
			t.Run(name, func(t *testing.T) {
				s := newEndedSession(t)
				dir := sessionEndTestDir(t)
				if reentry {
					dropFirstConnection(t, dir)
				}
				cfg := s.config(dir)
				cfg.Spawn = func(string) error { return &endingError{end: s.end(way)} }

				att, err := establish(s.ctx, cfg, s.input.ended)

				require.Nil(t, att)
				var failure *connectionSetupError
				require.False(t, errors.As(err, &failure), "a session end classified as a %v failure", err)
				if way == "stdin EOF" {
					require.ErrorIs(t, err, errSessionEnded)
				} else {
					require.ErrorIs(t, err, context.Canceled)
				}
			})
		}
	}
}

func TestEndWithoutAttach_ReturnsAnUnclassifiedFailureWithoutFallingBack(t *testing.T) {
	s := newEndedSession(t)
	cfg := s.config(sessionEndTestDir(t))
	failure := errors.New("not a discovery failure")

	err := endWithoutAttach(s.ctx, cfg, failure, s.input)

	require.ErrorIs(t, err, failure)
	assert.Zero(t, s.fallbacks.Load())
	assert.Zero(t, s.warnings.Load())
}

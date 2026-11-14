package sessionproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// hostInput reads host stdin from the start of discovery. Until the session
// attaches it holds every complete line, in order, so a host that writes ahead
// does not block while the shared server starts. Once attached it hands each
// line over directly, so a shared server that stops reading pushes back on the
// host instead of growing the held lines.
type hostInput struct {
	mu       sync.Mutex
	held     [][]byte
	attached bool
	readErr  error
	live     chan []byte
	ended    chan struct{}
	stopOnce sync.Once
	stopped  chan struct{}
}

func readHostInput(stdin io.Reader) *hostInput {
	in := &hostInput{live: make(chan []byte), ended: make(chan struct{}), stopped: make(chan struct{})}
	go in.read(stdin)
	return in
}

// stop releases the held lines and makes the reader drop every line it gets
// from now on. A read already blocked inside stdin cannot be interrupted
// without closing stdin, which belongs to the caller, so the reader may
// outlive the session until that read returns; it then reads no further.
func (in *hostInput) stop() {
	in.stopOnce.Do(func() {
		in.mu.Lock()
		defer in.mu.Unlock()
		close(in.stopped)
		in.held = nil
	})
}

// read discards an incomplete trailing line: only complete lines are
// forwarded.
func (in *hostInput) read(stdin io.Reader) {
	reader := bufio.NewReader(stdin)
	for !in.isStopped() {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			in.mu.Lock()
			in.readErr = err
			in.mu.Unlock()
			close(in.ended)
			return
		}
		if !in.deliver(line) {
			return
		}
	}
}

func (in *hostInput) isStopped() bool {
	select {
	case <-in.stopped:
		return true
	default:
		return false
	}
}

func (in *hostInput) deliver(line []byte) bool {
	in.mu.Lock()
	if in.isStopped() {
		in.mu.Unlock()
		return false
	}
	if !in.attached {
		in.held = append(in.held, line)
		in.mu.Unlock()
		return true
	}
	in.mu.Unlock()
	select {
	case in.live <- line:
		return true
	case <-in.stopped:
		return false
	}
}

// attach ends holding: the lines held so far are returned, in order, and every
// later line goes through live.
func (in *hostInput) attach() [][]byte {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.attached = true
	held := in.held
	in.held = nil
	return held
}

// forwardTo writes the held lines, then live ones, until host stdin ends,
// which is a normal end of the session.
func (in *hostInput) forwardTo(ctx context.Context, w io.Writer) error {
	if err := writeLines(w, in.attach()); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case line := <-in.live:
			if err := writeLines(w, [][]byte{line}); err != nil {
				return err
			}
		case <-in.ended:
			return in.endErr()
		}
	}
}

func (in *hostInput) endErr() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if errors.Is(in.readErr, io.EOF) {
		return nil
	}
	return fmt.Errorf("failed to read host input: %w", in.readErr)
}

func writeLines(w io.Writer, lines [][]byte) error {
	for _, line := range lines {
		if _, err := w.Write(line); err != nil {
			return fmt.Errorf("failed to relay to the shared server: %w", err)
		}
	}
	return nil
}

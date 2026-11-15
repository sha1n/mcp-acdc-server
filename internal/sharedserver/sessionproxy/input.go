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
// host instead of growing the held lines. A crash detaches it, and it holds
// again until the next attach.
type hostInput struct {
	mu       sync.Mutex
	held     [][]byte
	attached bool
	detached chan struct{}
	heldSent func(held int)
	readErr  error
	tail     []byte
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
			in.tail = line
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

// deliver holds a line whose hand-off a detach interrupted, unless the input
// was attached again in the meantime.
func (in *hostInput) deliver(line []byte) bool {
	for {
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
		detached := in.detached
		in.mu.Unlock()
		select {
		case in.live <- line:
			return true
		case <-detached:
		case <-in.stopped:
			return false
		}
	}
}

// attach ends holding: the lines held so far are returned, in order, and every
// later line goes through live until detach.
func (in *hostInput) attach() [][]byte {
	held, _ := in.attachForwarding()
	return held
}

func (in *hostInput) attachForwarding() ([][]byte, func(int)) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.attached = true
	in.detached = make(chan struct{})
	held := in.held
	in.held = nil
	heldSent := in.heldSent
	in.heldSent = nil
	return held, heldSent
}

// detach resumes holding; a line already waiting for the forwarder is held
// too.
func (in *hostInput) detach() {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.attached {
		in.attached = false
		close(in.detached)
	}
}

// onHeldSent registers a call for the next forwardTo once it has written the
// held lines.
func (in *hostInput) onHeldSent(heldSent func(held int)) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.heldSent = heldSent
}

// forwardTo writes the held lines, then live ones, until host stdin ends,
// which is a normal end of the session, or until ctx ends. Host input is held
// again from its return on, however late the forwarder started.
func (in *hostInput) forwardTo(ctx context.Context, w io.Writer) error {
	held, heldSent := in.attachForwarding()
	defer in.detach()
	if err := in.writeLines(w, held); err != nil {
		return err
	}
	if heldSent != nil {
		heldSent(len(held))
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case line := <-in.live:
			if err := in.writeLines(w, [][]byte{line}); err != nil {
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

// writeLines holds again, ahead of everything held, the lines from the one
// whose write failed: a line not fully written is not in flight, so the next
// shared server gets it.
func (in *hostInput) writeLines(w io.Writer, lines [][]byte) error {
	for i, line := range lines {
		if _, err := w.Write(line); err != nil {
			in.requeue(lines[i:])
			return fmt.Errorf("failed to relay to the shared server: %w", err)
		}
	}
	return nil
}

func (in *hostInput) requeue(lines [][]byte) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.held = append(append([][]byte(nil), lines...), in.held...)
}

// remainder yields the incomplete line read before host stdin ended, once,
// and then the read's result.
func (in *hostInput) remainder() ([]byte, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	tail := in.tail
	in.tail = nil
	if len(tail) > 0 {
		return tail, nil
	}
	return nil, in.readErr
}

// handOver gives the host input to a per-session server as a stream: the
// held lines, then live lines, then the incomplete last line and the read's
// result, as a per-session server reading stdin itself would see them.
// Holding continues until the first Read, so host EOF and cancellation stay
// observable while the per-session server starts; from then on the host is
// back-pressured. Close stops the reader and ends a pending Read.
func (in *hostInput) handOver() io.ReadCloser {
	return &handedOverInput{in: in}
}

type handedOverInput struct {
	in      *hostInput
	started bool
	queued  [][]byte
	pending []byte
}

func (h *handedOverInput) Read(p []byte) (int, error) {
	if !h.started {
		h.started = true
		h.queued = h.in.attach()
	}
	for len(h.pending) == 0 {
		line, err := h.next()
		if err != nil {
			return 0, err
		}
		h.pending = line
	}
	n := copy(p, h.pending)
	h.pending = h.pending[n:]
	return n, nil
}

func (h *handedOverInput) next() ([]byte, error) {
	if len(h.queued) > 0 {
		line := h.queued[0]
		h.queued = h.queued[1:]
		return line, nil
	}
	select {
	case line := <-h.in.live:
		return line, nil
	case <-h.in.ended:
		return h.in.remainder()
	case <-h.in.stopped:
		return nil, io.ErrClosedPipe
	}
}

func (h *handedOverInput) Close() error {
	h.in.stop()
	return nil
}

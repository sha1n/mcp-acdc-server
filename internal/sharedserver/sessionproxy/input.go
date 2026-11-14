package sessionproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// hostInput reads host stdin from the start of discovery and holds every
// complete line, in order, until it is relayed. Holding instead of leaving the
// bytes in the pipe keeps a host that writes ahead from blocking while the
// shared server starts.
type hostInput struct {
	mu      sync.Mutex
	lines   [][]byte
	readErr error
	pending chan struct{}
	ended   chan struct{}
}

func readHostInput(stdin io.Reader) *hostInput {
	in := &hostInput{pending: make(chan struct{}, 1), ended: make(chan struct{})}
	go in.read(stdin)
	return in
}

// read discards an incomplete trailing line: only complete lines are
// forwarded.
func (in *hostInput) read(stdin io.Reader) {
	reader := bufio.NewReader(stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			in.mu.Lock()
			in.readErr = err
			in.mu.Unlock()
			close(in.ended)
			return
		}
		in.mu.Lock()
		in.lines = append(in.lines, line)
		in.mu.Unlock()
		select {
		case in.pending <- struct{}{}:
		default:
		}
	}
}

func (in *hostInput) take() [][]byte {
	in.mu.Lock()
	defer in.mu.Unlock()
	lines := in.lines
	in.lines = nil
	return lines
}

// forwardTo writes the held lines, then live ones, until host stdin ends,
// which is a normal end of the session.
func (in *hostInput) forwardTo(ctx context.Context, w io.Writer) error {
	for {
		ended := false
		select {
		case <-ctx.Done():
			return nil
		case <-in.pending:
		case <-in.ended:
			ended = true
		}
		if err := writeLines(w, in.take()); err != nil {
			return err
		}
		if ended {
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

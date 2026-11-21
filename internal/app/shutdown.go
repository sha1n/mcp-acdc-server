package app

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// DefaultShutdownGrace is how long a server waits for in-flight handlers
// before force-closing their connections.
const DefaultShutdownGrace = 5 * time.Second

// notifyShutdownSignals returns a context that ends on the first SIGINT or
// SIGTERM, or when ctx ends. Every later SIGINT or SIGTERM exits the process
// at once with code 128+signo, so a second signal ends a shutdown that waits
// on a handler or a build that ignores cancellation. The returned CancelFunc
// unregisters the signals and ends the context.
func notifyShutdownSignals(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	// The registration is kept past the first signal rather than restoring
	// the default dispositions: those do not end the process when SIGINT was
	// inherited ignored or for SIGTERM as PID 1, and a repeat sent before the
	// restore would be swallowed.
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	e := &signalEscalator{done: make(chan struct{}), finished: make(chan struct{})}
	go e.run(signals, cancel)

	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			e.stop()
			signal.Stop(signals)
			close(e.done)
			cancel()
			<-e.finished
		})
	}
}

// signalEscalator exits the process on every signal after the first. Its
// exit decision and stop are serialized by mu so that no exit can happen once
// stop has returned, even for a signal already buffered when stop began.
type signalEscalator struct {
	mu       sync.Mutex
	stopped  bool
	done     chan struct{}
	finished chan struct{}
}

func (e *signalEscalator) run(signals <-chan os.Signal, cancel context.CancelFunc) {
	defer close(e.finished)
	signalled := false
	for {
		select {
		case <-e.done:
			return
		case sig := <-signals:
			if signalled {
				e.exitUnlessStopped(sig)
				return
			}
			signalled = true
			cancel()
		}
	}
}

func (e *signalEscalator) exitUnlessStopped(sig os.Signal) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.stopped {
		os.Exit(exitCodeForSignal(sig))
	}
}

func (e *signalEscalator) stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopped = true
}

func exitCodeForSignal(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return 1
}

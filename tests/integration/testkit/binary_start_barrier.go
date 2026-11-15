//go:build unix

package testkit

import (
	"os"
	"testing"
)

// releasedByBarrier blocks the shell on the inherited barrier descriptor
// until it reads end of file, closes it so the binary and the shared server
// it spawns do not inherit it, then becomes the binary under the same pid.
const releasedByBarrier = `read _ <&3; exec 3<&-; exec "$@"`

// StartBarrier holds launches before they exec the binary and releases them
// together. Every launch inherits the read end of one pipe at fork, so no
// launch can miss the release: closing the only write end delivers end of
// file to every blocked reader at once.
type StartBarrier struct {
	read, write *os.File
}

func NewStartBarrier(t testing.TB) *StartBarrier {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("barrier pipe: %v", err)
	}
	b := &StartBarrier{read: read, write: write}
	t.Cleanup(func() { _ = b.Release() })
	return b
}

// Release lets every launch behind the barrier exec the binary.
func (b *StartBarrier) Release() error {
	_ = b.read.Close()
	return b.write.Close()
}

// StartBehind launches the binary as Start does, held behind barrier until
// it is released.
func (e *BinaryEnv) StartBehind(t testing.TB, barrier *StartBarrier, args ...string) *BinaryLaunch {
	t.Helper()
	l := e.newLaunch(args)
	l.cmd.Path = "/bin/sh"
	l.cmd.Args = append([]string{"sh", "-c", releasedByBarrier, "sh", e.Binary}, args...)
	l.cmd.ExtraFiles = []*os.File{barrier.read}
	l.cmd.Stderr = l.stderr
	stdout, err := l.cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	l.start(t, stdout, nil, l.relayStdoutLine)
	return l
}

package testkit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

var errMalformedProcArgs = errors.New("malformed kern.procargs2")

func processArgs(pid int) ([]string, error) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, fmt.Errorf("kern.procargs2 of %d: %w", pid, err)
	}
	return parseProcArgs2(buf)
}

// parseProcArgs2 reads argc as a native int32, then skips the executable
// path and the NULs that pad it, to read argc NUL-terminated arguments; the
// environment follows them.
func parseProcArgs2(buf []byte) ([]string, error) {
	if len(buf) < 4 {
		return nil, errMalformedProcArgs
	}
	argc := int(int32(binary.NativeEndian.Uint32(buf)))
	execPathEnd := bytes.IndexByte(buf[4:], 0)
	if argc < 1 || execPathEnd < 0 {
		return nil, errMalformedProcArgs
	}
	rest := bytes.TrimLeft(buf[4+execPathEnd:], "\x00")
	args := make([]string, 0, argc)
	for range argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return nil, errMalformedProcArgs
		}
		args = append(args, string(rest[:end]))
		rest = rest[end+1:]
	}
	return args, nil
}

//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
)

// peerDisconnected reports whether err is how a departing session proxy
// surfaces: which of these appears depends on timing and platform (Linux
// resets a connection closed with unread data), so all of them are a plain
// session end rather than a failure.
func peerDisconnected(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, context.Canceled)
}

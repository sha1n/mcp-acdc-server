//go:build unix

package sharedserver

import (
	"context"
	"net"
)

// browse answers a browse request once the build is done, with the Web UI
// URL or the error that prevented the UI, and closes the connection. It is
// not an attached session: it gets no session id and no attach entry. Like
// one, though, it stops the linger while it is pending, so a cold start whose
// build outlasts the linger still answers it, and its end restarts the linger
// as the last detach does, so the page it opened has a whole linger to send
// its first heartbeat.
func (a *sessionAcceptor) browse(conn *net.UnixConn, maxResults int) {
	if !a.linger.hold() {
		a.dropOnceAcceptingStopped(conn)
		return
	}
	defer a.linger.release()
	if !a.awaitBuilt() {
		a.dropOnceAcceptingStopped(conn)
		return
	}
	defer func() { _ = conn.Close() }()
	stopOnCancel := context.AfterFunc(a.sessionCtx, func() { _ = conn.Close() })
	defer stopOnCancel()

	line, err := a.browseReply(maxResults)
	if err != nil {
		a.logger.Error("Failed to answer browse request", "error", err)
		return
	}
	if _, err := conn.Write(line); err != nil && !peerDisconnected(err) && a.sessionCtx.Err() == nil {
		a.logger.Warn("Failed to send browse reply", "error", err)
	}
}

// awaitBuilt reports whether the build produced a server before the shared
// server began to stop.
func (a *sessionAcceptor) awaitBuilt() bool {
	select {
	case <-a.built:
		return a.server != nil
	case <-a.ctx.Done():
		return false
	}
}

// browseReply starts the Web UI if needed. A UI that cannot start is
// reported to the requester only: attached sessions are unaffected and the
// next request tries again.
func (a *sessionAcceptor) browseReply(maxResults int) ([]byte, error) {
	uiURL, err := a.ui.ensure(maxResults)
	if err != nil {
		a.logger.Error("Failed to start the web UI", "error", err)
		return EncodeBrowseErrorLine(err.Error(), a.logPath)
	}
	return EncodeBrowseReadyLine(uiURL, a.logPath)
}

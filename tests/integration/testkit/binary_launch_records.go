//go:build unix

package testkit

import (
	"fmt"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
)

const attachMsg = "Attached to the shared server"

// AwaitStderrRecords polls stderr until n slog records match match and
// returns the first n of them in write order.
func (l *BinaryLaunch) AwaitStderrRecords(match slogtext.Record, n int, deadline time.Time) ([]slogtext.Record, error) {
	for {
		if found := matchingStderrRecords(l.Stderr(), match); len(found) >= n {
			return found[:n], nil
		}
		select {
		case <-l.done:
			if found := matchingStderrRecords(l.Stderr(), match); len(found) >= n {
				return found[:n], nil
			}
			return nil, fmt.Errorf("the process exited with fewer than %d stderr records %v; stderr:\n%s", n, match, l.Stderr())
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("fewer than %d stderr records %v before the deadline; stderr:\n%s", n, match, l.Stderr())
		}
	}
}

// AwaitNthAttach returns the session id and log path of the launch's nth
// attach line, counted from 1: a session writes one per attach, its first
// connect and every recovery.
func (l *BinaryLaunch) AwaitNthAttach(n int, deadline time.Time) (session, logPath string, err error) {
	records, err := l.AwaitStderrRecords(slogtext.Record{"msg": attachMsg}, n, deadline)
	if err != nil {
		return "", "", err
	}
	return records[n-1]["session"], records[n-1]["log"], nil
}

//go:build unix

package testkit

import (
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
)

const attachMsg = "Attached to the shared server"

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

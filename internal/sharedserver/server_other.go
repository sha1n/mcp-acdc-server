//go:build !unix

package sharedserver

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("shared server requires a Unix platform")

// Run is unavailable without Unix domain sockets and flock.
func Run(context.Context, Config) error {
	return errUnsupported
}

// EnsureEndpointDir is unavailable without Unix file ownership.
func EnsureEndpointDir(string) error {
	return errUnsupported
}

//go:build !unix

package sharedserver

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("shared server requires a Unix platform")

func Run(context.Context, Config) error {
	return errUnsupported
}

func EnsureEndpointDir(string) error {
	return errUnsupported
}

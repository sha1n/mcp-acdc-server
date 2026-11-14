// Package endpoint owns the verified endpoint directory of shared servers.
// It is a separate package so that no other code can build an Endpoint
// without the verification of OpenEndpoint.
package endpoint

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
)

// ErrSocketPathTooLong reports a socket path the platform cannot bind.
var ErrSocketPathTooLong = errors.New("socket path exceeds the platform limit")

var errEndpointNotOpened = errors.New("endpoint directory was not opened with OpenEndpoint")

// Endpoint is a verified endpoint directory. Socket and lock paths are
// obtained only through it, so neither can be built from an unverified
// directory; the zero value yields none.
type Endpoint struct {
	dir string
}

// SocketPath returns the socket path of the shared server for identity,
// rejecting a path longer than the platform allows.
func (e Endpoint) SocketPath(identity string) (string, error) {
	path, err := e.identityPath(identity, ".sock")
	if err != nil {
		return "", err
	}
	if err := checkSocketPathLength(runtime.GOOS, path); err != nil {
		return "", err
	}
	return path, nil
}

// LockPath returns the lock file path of the shared server for identity.
func (e Endpoint) LockPath(identity string) (string, error) {
	return e.identityPath(identity, ".lock")
}

func (e Endpoint) identityPath(identity, suffix string) (string, error) {
	if e.dir == "" {
		return "", errEndpointNotOpened
	}
	if !ValidIdentity(identity) {
		return "", fmt.Errorf("invalid shared server identity %q", identity)
	}
	return filepath.Join(e.dir, identity+suffix), nil
}

func checkSocketPathLength(goos, path string) error {
	limit := socketPathLimit(goos)
	if len(path) > limit {
		return fmt.Errorf("%w: %d bytes, limit %d: %s", ErrSocketPathTooLong, len(path), limit, path)
	}
	return nil
}

// socketPathLimit is the longest path Go binds: sun_path is 104 bytes on
// darwin and 108 on linux, one of which Go reserves.
func socketPathLimit(goos string) int {
	if goos == "darwin" {
		return 103
	}
	return 107
}

// ValidIdentity reports whether s is a server identity: 32 lowercase hex
// characters. Validating it keeps the identity from escaping the endpoint
// directory when it is used in a file name.
func ValidIdentity(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

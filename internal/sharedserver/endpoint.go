package sharedserver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

const endpointDirName = "acdc-mcp"

// ErrSocketPathTooLong reports a socket path the platform cannot bind.
var ErrSocketPathTooLong = errors.New("socket path exceeds the platform limit")

type endpointPlatform struct {
	goos         string
	getenv       func(string) string
	tempDir      func() string
	userCacheDir func() (string, error)
	uid          int
}

// DefaultEndpointDir returns the per-user directory that holds the socket and
// lock files of every shared server. It does not create the directory.
func DefaultEndpointDir() (string, error) {
	return resolveEndpointDir(endpointPlatform{
		goos:         runtime.GOOS,
		getenv:       os.Getenv,
		tempDir:      os.TempDir,
		userCacheDir: os.UserCacheDir,
		uid:          os.Getuid(),
	})
}

func resolveEndpointDir(p endpointPlatform) (string, error) {
	switch p.goos {
	case "linux":
		if runtimeDir := p.getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
			return filepath.Join(runtimeDir, endpointDirName), nil
		}
		return filepath.Join(p.tempDir(), endpointDirName+"-"+strconv.Itoa(p.uid)), nil
	case "darwin":
		cacheDir, err := p.userCacheDir()
		if err != nil {
			return "", fmt.Errorf("failed to resolve endpoint directory: %w", err)
		}
		return filepath.Join(cacheDir, endpointDirName), nil
	default:
		return "", fmt.Errorf("shared server is not supported on %s", p.goos)
	}
}

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

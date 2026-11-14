package sharedserver

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

const endpointDirName = "acdc-mcp"

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

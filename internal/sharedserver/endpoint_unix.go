//go:build unix

package sharedserver

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

const endpointDirMode fs.FileMode = 0o700

// EnsureEndpointDir creates dir if missing and verifies it is a directory
// owned by the current user with mode 0700. The directory mode is the only
// protection of the socket, so an existing directory that fails the check is
// rejected, never repaired. A non-absolute dir is rejected before anything is
// created: a relative one would resolve against the working directory, and on
// Linux an @-prefixed socket path binds an abstract socket that the directory
// mode does not protect.
func EnsureEndpointDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("endpoint directory %q is not an absolute path", dir)
	}
	if err := os.MkdirAll(dir, endpointDirMode); err != nil {
		return fmt.Errorf("failed to create endpoint directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("failed to inspect endpoint directory: %w", err)
	}
	if err := verifyEndpointDir(info, os.Getuid()); err != nil {
		return fmt.Errorf("endpoint directory %s: %w", dir, err)
	}
	return nil
}

func verifyEndpointDir(info fs.FileInfo, uid int) error {
	if !info.IsDir() {
		return errors.New("not a directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("owner cannot be determined")
	}
	if int(stat.Uid) != uid {
		return fmt.Errorf("owned by uid %d, not the current user %d", stat.Uid, uid)
	}
	if perm := info.Mode().Perm(); perm != endpointDirMode {
		return fmt.Errorf("mode %#o, want %#o", perm, endpointDirMode)
	}
	return nil
}

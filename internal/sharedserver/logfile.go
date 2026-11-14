package sharedserver

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	logDirName = "acdc-mcp"
	logSuffix  = ".log"
	// The thresholds are fixed by the spec and intentionally not configurable.
	logRotateSize = 10 << 20
	logRetention  = 7 * 24 * time.Hour
)

type logPlatform struct {
	goos        string
	getenv      func(string) string
	userHomeDir func() (string, error)
}

// DefaultLogDir returns the per-user directory that holds the log file of
// every shared server. It does not create the directory.
func DefaultLogDir() (string, error) {
	return resolveLogDir(logPlatform{
		goos:        runtime.GOOS,
		getenv:      os.Getenv,
		userHomeDir: os.UserHomeDir,
	})
}

func resolveLogDir(p logPlatform) (string, error) {
	switch p.goos {
	case "linux":
		if stateHome := p.getenv("XDG_STATE_HOME"); stateHome != "" {
			return filepath.Join(stateHome, logDirName), nil
		}
		home, err := p.userHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to resolve log directory: %w", err)
		}
		return filepath.Join(home, ".local", "state", logDirName), nil
	case "darwin":
		home, err := p.userHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to resolve log directory: %w", err)
		}
		return filepath.Join(home, "Library", "Logs", logDirName), nil
	default:
		return "", fmt.Errorf("no shared server log directory on %s", p.goos)
	}
}

func logPath(dir, identity string) string {
	return filepath.Join(dir, identity+logSuffix)
}

// rotateLog keeps one previous generation: an oversized log replaces any
// existing <id>.log.1.
func rotateLog(dir, identity string) error {
	current := logPath(dir, identity)
	info, err := os.Lstat(current)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect log for rotation: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= logRotateSize {
		return nil
	}
	if err := os.Rename(current, current+".1"); err != nil {
		return fmt.Errorf("failed to rotate log: %w", err)
	}
	return nil
}

// pruneLogs deletes the stale logs of every identity, not only this one, so
// identities that are never started again do not leave files behind forever.
func pruneLogs(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to list log directory: %w", err)
	}
	cutoff := time.Now().Add(-logRetention)
	var errs []error
	for _, entry := range entries {
		if !isPrunableLog(entry, cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("failed to delete stale log: %w", err))
		}
	}
	return errors.Join(errs...)
}

func isPrunableLog(entry fs.DirEntry, cutoff time.Time) bool {
	name := entry.Name()
	if !strings.HasSuffix(name, logSuffix) && !strings.HasSuffix(name, logSuffix+".1") {
		return false
	}
	if !entry.Type().IsRegular() {
		return false
	}
	info, err := entry.Info()
	return err == nil && info.ModTime().Before(cutoff)
}

//go:build unix

package sharedserver

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
)

// openLog applies rotation and retention, then opens the identity's log for
// appending. A nil file means the log is unavailable; the returned errors are
// maintenance failures to report once a log can be written, or the reason the
// log is unavailable.
func openLog(dir, identity string) (*os.File, []error) {
	// A relative directory would put the log under whatever the working
	// directory happens to be, and the ready line must carry an absolute path.
	if !filepath.IsAbs(dir) {
		return nil, []error{fmt.Errorf("log directory %q is not an absolute path", dir)}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, []error{fmt.Errorf("failed to create log directory: %w", err)}
	}

	var issues []error
	if err := rotateLog(dir, identity); err != nil {
		issues = append(issues, err)
	}
	if err := pruneLogs(dir); err != nil {
		issues = append(issues, err)
	}

	file, err := os.OpenFile(logPath(dir, identity), os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, append(issues, fmt.Errorf("failed to open log file: %w", err))
	}
	return file, issues
}

// startLog makes the identity's log, or a discarding logger when it cannot be
// opened, the process default for the shared server's lifetime: the packages
// it runs log through slog.Default. stop restores the previous default and
// closes the file.
func startLog(dir, identity string) (logger *slog.Logger, path string, stop func()) {
	previous := slog.Default()
	file, issues := openLog(dir, identity)
	if file == nil {
		logger = slog.New(slog.DiscardHandler)
		slog.SetDefault(logger)
		return logger, "", func() { slog.SetDefault(previous) }
	}

	logger = slog.New(slog.NewTextHandler(file, nil))
	slog.SetDefault(logger)
	for _, issue := range issues {
		logger.Warn("Shared server log maintenance failed", "error", issue)
	}
	return logger, file.Name(), func() {
		slog.SetDefault(previous)
		_ = file.Close()
	}
}

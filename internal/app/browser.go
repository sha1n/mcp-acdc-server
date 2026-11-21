package app

import (
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// browserOpenerWait bounds how long browse waits for the opener command to
// exit; an opener still running then is left to run on its own.
const browserOpenerWait = 5 * time.Second

// errBrowserOpenUnconfirmed is returned when the opener is still running
// after the wait, so whether the browser opened is unknown.
var errBrowserOpenUnconfirmed = errors.New("the browser launch could not be confirmed")

// browserCommand returns the command that opens a URL in the system browser
// on goos, or "" when browse does not open a browser there.
func browserCommand(goos string) string {
	switch goos {
	case "darwin":
		return "open"
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly":
		return "xdg-open"
	default:
		return ""
	}
}

// browserOpener returns a function that opens a URL in the system browser
// of goos.
func browserOpener(goos string, wait time.Duration) func(string) error {
	command := browserCommand(goos)
	return func(url string) error {
		if command == "" {
			return fmt.Errorf("no browser opener on %s", goos)
		}
		return openWith(command, url, wait)
	}
}

// openWith runs command on url and waits up to wait for it to exit; an
// opener still running then is left running and reported as
// errBrowserOpenUnconfirmed. The opener's stdin, stdout and stderr are the
// null device, so neither it nor a browser it starts holds the pipes of
// browse open after browse exits.
func openWith(command, url string, wait time.Duration) error {
	cmd := exec.Command(command, url) //nolint:gosec // a fixed opener on the URL of the Web UI
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to run %s: %w", command, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%s failed: %w", command, err)
		}
		return nil
	case <-time.After(wait):
		return fmt.Errorf("%s still running after %s: %w", command, wait, errBrowserOpenUnconfirmed)
	}
}

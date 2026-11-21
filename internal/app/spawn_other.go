//go:build !unix

package app

import "errors"

// spawnSharedServer is never reached on these platforms: every launch there
// is a per-session server.
func spawnSharedServer(string) error {
	return errors.New("a shared server cannot be spawned on this platform")
}

func spawnSharedServerWith([]string) func(string) error {
	return spawnSharedServer
}

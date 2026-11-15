//go:build unix

package testkit

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// WriteRecordingStub writes into dir an executable that never becomes a
// shared server: it records the identity it was spawned with, the argument
// after the prepended identity flag, and exits 0. identityFile appears once
// the first candidate has run, published atomically.
func WriteRecordingStub(dir string) (stub, identityFile string, err error) {
	stub = filepath.Join(dir, "recording-stub")
	identityFile = filepath.Join(dir, "spawned-identity")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$2\" > %[1]q.$$ && mv %[1]q.$$ %[1]q\nexit 0\n", identityFile)
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		return "", "", fmt.Errorf("failed to write the recording stub: %w", err)
	}
	return stub, identityFile, nil
}

// AwaitFile returns the content of path once it exists.
func AwaitFile(path string, deadline time.Time) (string, error) {
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("%s did not appear before the deadline", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// UseBinaryCopy makes every later launch of this environment run its own copy
// of the binary, which the test may replace without affecting other tests.
func (e *BinaryEnv) UseBinaryCopy(t testing.TB) {
	t.Helper()
	binDir := filepath.Join(e.Root, "b")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatalf("failed to create the binary dir: %v", err)
	}
	binary := filepath.Join(binDir, "acdc-mcp")
	if err := copyExecutable(e.Binary, binary); err != nil {
		t.Fatalf("failed to copy the binary: %v", err)
	}
	e.Binary = binary
}

// ReplaceBinary renames a copy of replacement over the environment's binary,
// as an upgrade on disk does: running processes keep the old file, and a
// later exec of the path runs the replacement.
func (e *BinaryEnv) ReplaceBinary(t testing.TB, replacement string) {
	t.Helper()
	staged := e.Binary + ".new"
	if err := copyExecutable(replacement, staged); err != nil {
		t.Fatalf("failed to stage the replacement: %v", err)
	}
	if err := os.Rename(staged, e.Binary); err != nil {
		t.Fatalf("failed to replace the binary: %v", err)
	}
}

func copyExecutable(from, to string) (err error) {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dst.Close(); err == nil {
			err = closeErr
		}
	}()
	_, err = io.Copy(dst, src)
	return err
}

// EndpointHarness binds the in-process fake endpoints to this environment's
// endpoint directory, so a test can serve a socket path real processes dial.
func (e *BinaryEnv) EndpointHarness(t testing.TB) *SharedServerHarness {
	return &SharedServerHarness{t: t, EndpointDir: e.EndpointDir(), LogDir: e.LogDir()}
}

// AwaitNoProcessWithArg waits until no process has arg among its arguments.
// A spawned candidate carries its spawning launch's arguments from the fork
// on, so a launch argument unique to the test names every process it started.
func AwaitNoProcessWithArg(arg string, deadline time.Time) error {
	for {
		pids, err := processesWithArg(arg)
		if err != nil {
			return err
		}
		if len(pids) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("processes %v with argument %q still run at the deadline", pids, arg)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func processesWithArg(arg string) ([]string, error) {
	out, err := exec.Command("ps", "-A", "-ww", "-o", "pid=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps failed: %w", err)
	}
	var pids []string
	for line := range strings.Lines(string(out)) {
		pid, args, _ := strings.Cut(strings.TrimSpace(line), " ")
		if containsArg(strings.Fields(args), arg) {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

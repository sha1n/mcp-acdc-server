package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStderr replaces os.Stderr for the duration of run, so the test must
// not run in parallel.
func captureStderr(t *testing.T, run func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stderr
	os.Stderr = writer
	t.Cleanup(func() { os.Stderr = original })
	captured := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(reader)
		captured <- string(out)
	}()
	run()
	os.Stderr = original
	require.NoError(t, writer.Close())
	out := <-captured
	require.NoError(t, reader.Close())
	return out
}

// Every error a launch returns, a failed session recovery included, takes
// this path: one "Error: " line on stderr and exit code 1.
func TestRunMain_ALaunchErrorExitsWithCode1AndOneErrorLine(t *testing.T) {
	exitCode := -1
	stderr := captureStderr(t, func() {
		runMain([]string{"cmd", "--transport", "sse", "--content-dir", "/non-existent"}, func(code int) { exitCode = code })
	})

	assert.Equal(t, 1, exitCode)
	var errorLines []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "Error: ") {
			errorLines = append(errorLines, line)
		}
	}
	require.Len(t, errorLines, 1, "stderr: %s", stderr)
	assert.Contains(t, errorLines[0], "/non-existent")
}

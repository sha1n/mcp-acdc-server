//go:build unix

package binary

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every candidate loses the held lock and exits 0, as an extra candidate of a
// slow cold start does. The binary is replaced by a wrapper that records each
// later candidate's pid and parent and execs the real binary in place, so the
// candidate stays the proxy's child. The proxy hashes its executable once,
// racing the replacement, so the locks of both possible identities are held.
func TestSessionProxy_ReapsLosingCandidates(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	env.UseBinaryCopy(t)
	wrapper, spawnsFile := writeSpawnRecordingWrapper(t, env)
	args := sharedLaunchArgs(testkit.CreateTestContentDir(t, nil))
	endpoints := env.EndpointHarness(t)
	for _, executable := range []string{env.Binary, wrapper} {
		endpoints.Binary = identity.Binary{Version: "dev", Build: "unknown", Executable: executable}
		endpoints.HoldLock(endpoints.Identity(args))
	}

	launch := env.Start(t, args...)
	reportReapDiagnosticsOnFailure(t, env, launch, spawnsFile)
	env.ReplaceBinary(t, wrapper)

	var spawns []recordedSpawn
	spawned := assert.Eventually(t, func() bool {
		spawns = readRecordedSpawns(t, spawnsFile)
		return len(spawns) >= 2
	}, time.Until(deadline), 20*time.Millisecond)
	require.True(t, spawned, "the proxy did not keep spawning candidates; stderr:\n%s", launch.Stderr())
	proxy := spawns[0].parent
	require.Eventually(t, func() bool { return allExited(t, spawns[:2]) }, time.Until(deadline), 20*time.Millisecond,
		"the recorded candidates %v never lost the held lock and exited", spawns[:2])

	var zombies []string
	assert.Eventually(t, func() bool {
		zombies = zombieChildren(t, proxy)
		return len(zombies) == 0
	}, 5*time.Second, 50*time.Millisecond, "losing candidates remain zombie children of the live session proxy %d", proxy)
	require.Empty(t, zombies, "zombie children (pid ppid stat) of the session proxy %d", proxy)
}

type recordedSpawn struct{ pid, parent int }

// The wrapper execs the real binary so the recorded pid and parent remain
// those of the candidate itself.
func writeSpawnRecordingWrapper(t *testing.T, env *testkit.BinaryEnv) (wrapper, spawnsFile string) {
	t.Helper()
	real := filepath.Join(env.Root, "real-acdc-mcp")
	data, err := os.ReadFile(env.Binary)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(real, data, 0o755))
	wrapper = filepath.Join(env.Root, "spawn-recording-wrapper")
	spawnsFile = filepath.Join(env.Root, "spawns")
	script := fmt.Sprintf("#!/bin/sh\necho \"$$ $PPID\" >> %q\nexec %q \"$@\"\n", spawnsFile, real)
	require.NoError(t, os.WriteFile(wrapper, []byte(script), 0o755))
	return wrapper, spawnsFile
}

func readRecordedSpawns(t *testing.T, spawnsFile string) []recordedSpawn {
	t.Helper()
	data, err := os.ReadFile(spawnsFile)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return parseSpawns(data)
}

func parseSpawns(data []byte) []recordedSpawn {
	var spawns []recordedSpawn
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var s recordedSpawn
		if _, err := fmt.Sscanf(line, "%d %d", &s.pid, &s.parent); err == nil {
			spawns = append(spawns, s)
		}
	}
	return spawns
}

// allExited reports whether every spawn is gone or a zombie, so that a
// running candidate is never mistaken for a reaped one.
func allExited(t *testing.T, spawns []recordedSpawn) bool {
	t.Helper()
	out, err := exec.Command("ps", "-A", "-o", "pid=,stat=").Output()
	require.NoError(t, err)
	running := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && !strings.HasPrefix(fields[1], "Z") {
			running[fields[0]] = true
		}
	}
	for _, s := range spawns {
		if running[strconv.Itoa(s.pid)] {
			return false
		}
	}
	return true
}

// zombieChildren lists "pid ppid stat" of every zombie whose parent is pid;
// ps reports the same columns on Linux and macOS.
func zombieChildren(t *testing.T, pid int) []string {
	t.Helper()
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,stat=").Output()
	require.NoError(t, err)
	var zombies []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[1] == strconv.Itoa(pid) && strings.HasPrefix(fields[2], "Z") {
			zombies = append(zombies, line)
		}
	}
	return zombies
}

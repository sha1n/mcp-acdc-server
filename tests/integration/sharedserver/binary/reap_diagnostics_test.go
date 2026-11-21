//go:build unix

package binary

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
)

// reportReapDiagnosticsOnFailure registers a cleanup that, when the test has
// failed, logs what a timeout alone does not tell: the recorded spawns with
// their process state, the proxy's stderr and the endpoint and log
// directories. Register it after the launch, so it runs before the launch is
// killed.
func reportReapDiagnosticsOnFailure(t *testing.T, env *testkit.BinaryEnv, launch *testkit.BinaryLaunch, spawnsFile string) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		var b strings.Builder
		data, _ := os.ReadFile(spawnsFile)
		fmt.Fprintf(&b, "reap diagnostics\n--- recorded spawns (pid ppid) and ps state\n%s", describeSpawns(parseSpawns(data)))
		fmt.Fprintf(&b, "--- proxy stderr\n%s\n", launch.Stderr())
		fmt.Fprintf(&b, "--- endpoint directory %s\n%s", env.EndpointDir(), describeDir(env.EndpointDir(), false))
		fmt.Fprintf(&b, "--- log directory %s\n%s", env.LogDir(), describeDir(env.LogDir(), true))
		t.Log(b.String())
	})
}

func describeSpawns(spawns []recordedSpawn) string {
	if len(spawns) == 0 {
		return "(none)\n"
	}
	var b strings.Builder
	for _, s := range spawns {
		out, err := exec.Command("ps", "-o", "pid=,ppid=,stat=,etime=,command=", "-p", strconv.Itoa(s.pid)).Output()
		state := strings.TrimSpace(string(out))
		if err != nil && state == "" {
			state = "gone"
		}
		fmt.Fprintf(&b, "%d %d: %s\n", s.pid, s.parent, state)
	}
	return b.String()
}

// describeDir lists every entry under dir with its mode and size, and with
// withContents the content of each regular file.
func describeDir(dir string, withContents bool) string {
	var b strings.Builder
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			fmt.Fprintf(&b, "%s: %v\n", path, err)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			fmt.Fprintf(&b, "%s: %v\n", path, err)
			return nil
		}
		fmt.Fprintf(&b, "%s %d %s\n", info.Mode(), info.Size(), path)
		if withContents && info.Mode().IsRegular() {
			data, _ := os.ReadFile(path)
			fmt.Fprintf(&b, "%s\n", data)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(&b, "%v\n", err)
	}
	return b.String()
}

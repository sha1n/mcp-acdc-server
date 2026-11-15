//go:build unix

package binary

import (
	"fmt"
	"os"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
)

var acdcBinary string

func TestMain(m *testing.M) {
	os.Exit(runWithBuiltBinary(m))
}

func runWithBuiltBinary(m *testing.M) int {
	dir, err := os.MkdirTemp("", "acdc-binary-tier-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "binary tier: failed to create the build dir: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	acdcBinary, err = testkit.BuildBinary(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "binary tier: failed to build acdc-mcp: %v\n", err)
		return 1
	}
	return m.Run()
}

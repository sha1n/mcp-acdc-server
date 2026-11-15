//go:build unix && !linux && !darwin

package testkit

import (
	"fmt"
	"runtime"
)

func processArgs(int) ([]string, error) {
	return nil, fmt.Errorf("reading the arguments of a process is not supported on %s", runtime.GOOS)
}

package testkit

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func processArgs(pid int) ([]string, error) {
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, err
	}
	return parseCmdline(cmdline)
}

func parseCmdline(cmdline []byte) ([]string, error) {
	if len(cmdline) == 0 {
		return nil, errors.New("the process has no argument vector")
	}
	return strings.Split(strings.TrimSuffix(string(cmdline), "\x00"), "\x00"), nil
}

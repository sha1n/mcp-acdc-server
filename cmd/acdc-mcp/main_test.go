package main

import (
	"testing"
)

func TestExecute_Help(t *testing.T) {
	// Test --help doesn't return error
	err := Execute("test", "test", "test", []string{"--help"})
	if err != nil {
		t.Errorf("Execute --help failed: %v", err)
	}
}

func TestExecute_UnknownFlag(t *testing.T) {
	// Test unknown flag returns error
	err := Execute("test", "test", "test", []string{"--unknown-flag"})
	if err == nil {
		t.Error("Expected error for unknown flag")
	}
}
func TestExecute_Run(t *testing.T) {
	// Trigger runWithFlags by providing a valid transport but invalid settings that cause early exit
	err := Execute("test", "test", "test", []string{"--transport", "sse", "--content-dir", "/non-existent"})
	if err == nil {
		t.Error("Expected error for non-existent content-dir")
	}
}

func TestRunMain(t *testing.T) {
	exitCode := -1
	exit := func(code int) {
		exitCode = code
	}

	// Success case
	runMain([]string{"cmd", "--help"}, exit)
	if exitCode != -1 {
		t.Errorf("Expected no exit call for --help, got exit(%d)", exitCode)
	}

	// Error case
	runMain([]string{"cmd", "--unknown-flag"}, exit)
	if exitCode != 1 {
		t.Errorf("Expected exit(1) for unknown flag, got exit(%d)", exitCode)
	}
}

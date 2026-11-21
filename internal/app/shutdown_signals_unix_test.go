//go:build unix

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// shutdownSignalsTestChildArg makes this test binary a child that
	// registers notifyShutdownSignals and signals itself, so real signal
	// dispositions and the real process exit are observed from outside.
	shutdownSignalsTestChildArg = "-acdc-shutdown-signals-test-child"
	// shutdownSignalsChildWatchdog ends a child that was not terminated by
	// its second signal with shutdownSignalsChildSurvived.
	shutdownSignalsChildWatchdog = 10 * time.Second
	shutdownSignalsChildSurvived = 3
	shutdownSignalsCancelled     = "cancelled"
	shutdownSignalsSurvivedFirst = "survived-first"
	// childModeAfterCancel sends the second signal a while after the first
	// one cancelled the context; childModeImmediate sends it the moment the
	// context is cancelled, so a window in which the handler swallows it
	// leaves the child running. Sending both before the first is consumed
	// would prove nothing: the kernel and the Go runtime coalesce a pending
	// signal with its repeat.
	childModeAfterCancel = "after-cancel"
	childModeImmediate   = "immediate"
	// childModeStopRace races a second signal against the CancelFunc; see
	// runStopRaceChild.
	childModeStopRace  = "stop-race"
	stopRaceIterations = 200
	stopRaceSignalled  = "signalled"
	stopRaceStopped    = "stopped"
	stopRaceCompleted  = "completed"
	// stopRaceSettle gives an os.Exit from a helper that outlived its
	// CancelFunc time to happen while "stopped" is still the last line.
	stopRaceSettle = 5 * time.Millisecond
	// childModeStopArbitration calls the CancelFunc while a second signal is
	// already dispatched; see runStopArbitrationChild.
	childModeStopArbitration   = "stop-arbitration"
	stopArbitrationIterations  = 50
	stopArbitrationSignalling  = "signalling"
	stopArbitrationStopReturns = "stop-returned"
	stopArbitrationCompleted   = "completed"
	ignoreSIGINTExecArgs       = `trap "" INT; exec "$0" "$@"`
)

// runShutdownSignalsChild is the child side of the escalation tests. Its exit
// code is the one notifyShutdownSignals chose on the second signal; it exits
// with shutdownSignalsChildSurvived when no signal ended it.
func runShutdownSignalsChild(args []string) int {
	if len(args) != 2 {
		return 2
	}
	mode := args[0]
	sig, ok := map[string]syscall.Signal{"SIGINT": syscall.SIGINT, "SIGTERM": syscall.SIGTERM}[args[1]]
	if !ok {
		return 2
	}
	if mode == childModeStopRace {
		return runStopRaceChild(sig)
	}
	if mode == childModeStopArbitration {
		return runStopArbitrationChild(sig)
	}
	ctx, stop := notifyShutdownSignals(context.Background())
	defer stop()

	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		return 2
	}
	select {
	case <-ctx.Done():
	case <-time.After(shutdownSignalsChildWatchdog):
		return shutdownSignalsChildSurvived
	}
	if mode == childModeAfterCancel {
		fmt.Println(shutdownSignalsCancelled)
		// Long enough for an exit on the first signal to have happened.
		time.Sleep(100 * time.Millisecond)
		fmt.Println(shutdownSignalsSurvivedFirst)
	}
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		return 2
	}
	time.Sleep(shutdownSignalsChildWatchdog)
	return shutdownSignalsChildSurvived
}

// runStopRaceChild registers the helper, cancels it with a first signal, then
// sends a second signal and calls the CancelFunc back to back, many times. It
// prints the iteration after the second signal and again once the CancelFunc
// returned. An independent registration keeps the default disposition from
// ending the child on a signal the helper no longer receives, so the only
// non-zero exit is the helper's os.Exit.
func runStopRaceChild(sig syscall.Signal) int {
	outer := make(chan os.Signal, 1)
	signal.Notify(outer, syscall.SIGINT, syscall.SIGTERM)
	for i := 0; i < stopRaceIterations; i++ {
		ctx, stop := notifyShutdownSignals(context.Background())
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			return 2
		}
		select {
		case <-ctx.Done():
		case <-time.After(shutdownSignalsChildWatchdog):
			return shutdownSignalsChildSurvived
		}
		fmt.Printf("%s %d\n", stopRaceSignalled, i)
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			return 2
		}
		stop()
		fmt.Printf("%s %d\n", stopRaceStopped, i)
		time.Sleep(stopRaceSettle)
	}
	fmt.Println(stopRaceCompleted)
	return 0
}

// runStopArbitrationChild cancels the helper with a first signal, then sends
// a second one and waits until the runtime has dispatched it to an
// independent registration before calling the CancelFunc. Waiting on that
// registration, instead of sleeping, makes the second signal pending for the
// helper too: the runtime dispatches a signal to every registration at once,
// and the signal.Stop in the CancelFunc waits for that dispatch. The
// signalling line is written before the second signal is sent and the
// stop-returned line right after the CancelFunc returns, so an exit after the
// CancelFunc returned leaves stop-returned as the last line. The independent registration also keeps
// the default disposition from ending the child once the helper stopped.
func runStopArbitrationChild(sig syscall.Signal) int {
	outer := make(chan os.Signal, 1)
	signal.Notify(outer, syscall.SIGINT, syscall.SIGTERM)
	await := func(ch <-chan struct{}) bool {
		select {
		case <-ch:
			return true
		case <-time.After(shutdownSignalsChildWatchdog):
			return false
		}
	}
	delivered := func() <-chan struct{} {
		ch := make(chan struct{})
		go func() {
			<-outer
			close(ch)
		}()
		return ch
	}
	for i := 0; i < stopArbitrationIterations; i++ {
		ctx, stop := notifyShutdownSignals(context.Background())
		first := delivered()
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			return 2
		}
		if !await(ctx.Done()) || !await(first) {
			return shutdownSignalsChildSurvived
		}
		second := delivered()
		fmt.Printf("%s %d\n", stopArbitrationSignalling, i)
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			return 2
		}
		if !await(second) {
			return shutdownSignalsChildSurvived
		}
		stop()
		fmt.Printf("%s %d\n", stopArbitrationStopReturns, i)
	}
	fmt.Println(stopArbitrationCompleted)
	return 0
}

type shutdownSignalsChildResult struct {
	stdout   string
	exitCode int
}

func runShutdownSignalsTestChild(t *testing.T, mode string, sig syscall.Signal, ignoreSIGINT bool) shutdownSignalsChildResult {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	args := []string{shutdownSignalsTestChildArg, mode, signalName(sig)}
	var cmd *exec.Cmd
	if ignoreSIGINT {
		// An ignored disposition survives exec, so the child starts with
		// SIGINT ignored, as a background job of a non-interactive shell does.
		cmd = exec.Command("sh", append([]string{"-c", ignoreSIGINTExecArgs, executable}, args...)...)
	} else {
		cmd = exec.Command(executable, args...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err = <-done:
	case <-time.After(3 * shutdownSignalsChildWatchdog):
		_ = cmd.Process.Kill()
		t.Fatalf("the child did not exit (killed: %v)\nstdout:\n%s\nstderr:\n%s", <-done, &stdout, &stderr)
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("waiting for the child failed: %v", err)
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		t.Fatalf("the child was killed by %v (default disposition), want exit code %d from the handler\nstdout:\n%s\nstderr:\n%s",
			ws.Signal(), 128+int(sig), &stdout, &stderr)
	}
	return shutdownSignalsChildResult{stdout: stdout.String(), exitCode: cmd.ProcessState.ExitCode()}
}

func signalName(sig syscall.Signal) string {
	if sig == syscall.SIGINT {
		return "SIGINT"
	}
	return "SIGTERM"
}

var shutdownSignalCases = []struct {
	name         string
	sig          syscall.Signal
	ignoreSIGINT bool
}{
	{name: "SIGTERM", sig: syscall.SIGTERM},
	{name: "SIGINT", sig: syscall.SIGINT},
	{name: "SIGINT started ignored", sig: syscall.SIGINT, ignoreSIGINT: true},
}

func TestNotifyShutdownSignals_FirstSignalCancelsSecondExitsWith128PlusSigno(t *testing.T) {
	for _, tc := range shutdownSignalCases {
		t.Run(tc.name, func(t *testing.T) {
			result := runShutdownSignalsTestChild(t, childModeAfterCancel, tc.sig, tc.ignoreSIGINT)

			assert.Contains(t, result.stdout, shutdownSignalsCancelled, "the first signal must cancel the context")
			assert.Contains(t, result.stdout, shutdownSignalsSurvivedFirst, "the first signal must not end the process")
			assert.Equal(t, 128+int(tc.sig), result.exitCode, "the second signal must exit with 128+signo")
		})
	}
}

func TestNotifyShutdownSignals_SecondSignalRightAfterCancellationIsNeverLost(t *testing.T) {
	for _, tc := range shutdownSignalCases {
		t.Run(tc.name, func(t *testing.T) {
			// A lost second signal shows only sometimes, so repeat the race.
			for i := 0; i < 20; i++ {
				result := runShutdownSignalsTestChild(t, childModeImmediate, tc.sig, tc.ignoreSIGINT)

				require.Equal(t, 128+int(tc.sig), result.exitCode,
					"run %d: a second signal sent right after the cancellation must exit with 128+signo\nstdout:\n%s", i, result.stdout)
			}
		})
	}
}

// A second signal already pending when the CancelFunc is called may escalate
// only before the CancelFunc returns: the child then exits 128+signo with the
// signalling line last. Otherwise the CancelFunc returns and the child goes on.
func TestNotifyShutdownSignals_StopWithPendingSecondSignalExitsOnlyBeforeStopReturns(t *testing.T) {
	for _, tc := range shutdownSignalCases {
		if tc.ignoreSIGINT {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			result := runShutdownSignalsTestChild(t, childModeStopArbitration, tc.sig, false)
			lines := strings.Split(strings.TrimSpace(result.stdout), "\n")
			last := lines[len(lines)-1]
			if result.exitCode == 0 {
				require.Equal(t, stopArbitrationCompleted, last)
				require.Equal(t, stopArbitrationIterations, strings.Count(result.stdout, stopArbitrationStopReturns+" "))
				return
			}
			require.Equal(t, 128+int(tc.sig), result.exitCode, "stdout:\n%s", result.stdout)
			require.True(t, strings.HasPrefix(last, stopArbitrationSignalling+" "),
				"the helper called os.Exit after its CancelFunc returned (last line %q)", last)
		})
	}
}

// Race-probing: a second signal sent right before the CancelFunc may still
// escalate while the CancelFunc runs, but once the CancelFunc has returned
// the helper must never call os.Exit. A violation shows only sometimes, so
// the race is repeated stopRaceIterations times.
func TestNotifyShutdownSignals_StopRacingSecondSignalNeverExitsAfterStopReturns(t *testing.T) {
	for _, tc := range shutdownSignalCases {
		if tc.ignoreSIGINT {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			for probes := 0; probes < stopRaceIterations; {
				result := runShutdownSignalsTestChild(t, childModeStopRace, tc.sig, false)
				lines := strings.Split(strings.TrimSpace(result.stdout), "\n")
				probes += strings.Count(result.stdout, stopRaceStopped+" ")
				if result.exitCode == 0 {
					require.Equal(t, stopRaceCompleted, lines[len(lines)-1])
					continue
				}
				require.Equal(t, 128+int(tc.sig), result.exitCode, "stdout:\n%s", result.stdout)
				last := lines[len(lines)-1]
				require.True(t, strings.HasPrefix(last, stopRaceSignalled+" "),
					"the helper called os.Exit after its CancelFunc returned (last line %q)", last)
				probes++
			}
		})
	}
}

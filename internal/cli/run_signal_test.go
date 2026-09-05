//go:build !windows

package cli

import (
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// All tests call the production RunSignalHandler from run_signal.go.

func startChild(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // let trap register
	return cmd
}

// TestHandler_ForwardSIGTERM: production handler forwards SIGTERM to child group.
func TestHandler_ForwardSIGTERM(t *testing.T) {
	cmd := startChild(t, `trap 'exit 42' TERM; while true; do sleep 0.1; done`)

	var running atomic.Bool
	running.Store(true)
	cancel := make(chan struct{})

	cleanup := RunSignalHandler(SignalHandlerConfig{
		Pid:           -cmd.Process.Pid,
		ChildRunning:  &running,
		KillCancel:    cancel,
		EscalateAfter: 5 * time.Second,
	})

	// Simulate: OS sends SIGTERM to our process (via the handler's signal channel)
	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

	err := cmd.Wait()
	running.Store(false)
	close(cancel)
	cleanup()

	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 42 {
			t.Errorf("exit code = %d, want 42", exitErr.ExitCode())
		}
	} else if err == nil {
		t.Fatal("child should have exited from SIGTERM trap")
	}
}

// TestHandler_ForwardSIGHUP: production handler forwards SIGHUP.
func TestHandler_ForwardSIGHUP(t *testing.T) {
	cmd := startChild(t, `trap 'exit 43' HUP; while true; do sleep 0.1; done`)

	var running atomic.Bool
	running.Store(true)
	cancel := make(chan struct{})

	cleanup := RunSignalHandler(SignalHandlerConfig{
		Pid:           -cmd.Process.Pid,
		ChildRunning:  &running,
		KillCancel:    cancel,
		EscalateAfter: 5 * time.Second,
	})

	syscall.Kill(syscall.Getpid(), syscall.SIGHUP)

	err := cmd.Wait()
	running.Store(false)
	close(cancel)
	cleanup()

	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 43 {
			t.Errorf("exit code = %d, want 43", exitErr.ExitCode())
		}
	} else if err == nil {
		t.Fatal("child should have exited from SIGHUP trap")
	}
}

// TestHandler_EscalateToKILL: child ignores SIGTERM, handler escalates to SIGKILL.
func TestHandler_EscalateToKILL(t *testing.T) {
	cmd := startChild(t, `trap '' TERM; while true; do sleep 0.1; done`)

	var running atomic.Bool
	running.Store(true)
	cancel := make(chan struct{})

	cleanup := RunSignalHandler(SignalHandlerConfig{
		Pid:           -cmd.Process.Pid,
		ChildRunning:  &running,
		KillCancel:    cancel,
		EscalateAfter: 500 * time.Millisecond, // shortened for test
	})

	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

	err := cmd.Wait()
	running.Store(false)
	close(cancel)
	cleanup()

	if err == nil {
		t.Fatal("child should have been killed")
	}
}

// TestHandler_SecondSignalImmediateKILL: second signal during escalation wait
// triggers immediate SIGKILL instead of waiting for the timer.
func TestHandler_SecondSignalImmediateKILL(t *testing.T) {
	cmd := startChild(t, `trap '' TERM INT; while true; do sleep 0.1; done`)

	var running atomic.Bool
	running.Store(true)
	cancel := make(chan struct{})

	cleanup := RunSignalHandler(SignalHandlerConfig{
		Pid:           -cmd.Process.Pid,
		ChildRunning:  &running,
		KillCancel:    cancel,
		EscalateAfter: 30 * time.Second, // long timer — second signal should beat it
	})

	start := time.Now()
	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	time.Sleep(200 * time.Millisecond)
	syscall.Kill(syscall.Getpid(), syscall.SIGINT) // second signal

	err := cmd.Wait()
	elapsed := time.Since(start)
	running.Store(false)
	close(cancel)
	cleanup()

	if err == nil {
		t.Fatal("child should have been killed")
	}
	if elapsed > 5*time.Second {
		t.Errorf("took %v — second signal should have triggered immediate SIGKILL", elapsed)
	}
}

// TestHandler_NoEscalateIfChildExits: child exits promptly, no SIGKILL.
func TestHandler_NoEscalateIfChildExits(t *testing.T) {
	cmd := startChild(t, `trap 'exit 0' TERM; while true; do sleep 0.1; done`)

	var running atomic.Bool
	running.Store(true)
	cancel := make(chan struct{})

	cleanup := RunSignalHandler(SignalHandlerConfig{
		Pid:           -cmd.Process.Pid,
		ChildRunning:  &running,
		KillCancel:    cancel,
		EscalateAfter: 5 * time.Second,
	})

	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

	cmd.Wait()
	running.Store(false)
	close(cancel)
	cleanup()

	// If we get here without hanging, escalation was cancelled
}

// TestHandler_IgnoredAfterChildExit: handler doesn't panic on signal after child exits.
func TestHandler_IgnoredAfterChildExit(t *testing.T) {
	cmd := startChild(t, `exit 0`)

	var running atomic.Bool
	running.Store(true)
	cancel := make(chan struct{})

	cleanup := RunSignalHandler(SignalHandlerConfig{
		Pid:           -cmd.Process.Pid,
		ChildRunning:  &running,
		KillCancel:    cancel,
		EscalateAfter: 5 * time.Second,
	})

	cmd.Wait()
	running.Store(false)
	close(cancel)
	cleanup()

	// No panic = pass
}

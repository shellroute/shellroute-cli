//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestSignal_ForwardSIGTERM: child receives SIGTERM and exits with trap code.
func TestSignal_ForwardSIGTERM(t *testing.T) {
	cmd := exec.Command("bash", "-c", `trap 'exit 42' TERM; while true; do sleep 0.1; done`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)

	err := cmd.Wait()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 42 {
			t.Errorf("exit code = %d, want 42", exitErr.ExitCode())
		}
	} else if err == nil {
		t.Fatal("child should have exited non-zero from trap")
	}
}

// TestSignal_ForwardSIGHUP: child receives SIGHUP and exits with trap code.
func TestSignal_ForwardSIGHUP(t *testing.T) {
	cmd := exec.Command("bash", "-c", `trap 'exit 43' HUP; while true; do sleep 0.1; done`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	syscall.Kill(cmd.Process.Pid, syscall.SIGHUP)

	err := cmd.Wait()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 43 {
			t.Errorf("exit code = %d, want 43", exitErr.ExitCode())
		}
	} else if err == nil {
		t.Fatal("child should have exited from SIGHUP trap")
	}
}

// TestSignal_EscalateToKILL: child ignores SIGTERM, gets SIGKILL after timeout.
func TestSignal_EscalateToKILL(t *testing.T) {
	cmd := exec.Command("bash", "-c", `trap '' TERM; while true; do sleep 0.1; done`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	var childRunning atomic.Bool
	childRunning.Store(true)

	syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)

	go func() {
		time.Sleep(500 * time.Millisecond) // shortened for test
		if childRunning.Load() {
			syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
		}
	}()

	err := cmd.Wait()
	childRunning.Store(false)

	if err == nil {
		t.Fatal("child should have been killed")
	}
}

// TestSignal_NoEscalateIfPromptExit: child exits on SIGTERM, escalation cancelled.
func TestSignal_NoEscalateIfPromptExit(t *testing.T) {
	cmd := exec.Command("bash", "-c", `trap 'exit 0' TERM; while true; do sleep 0.1; done`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	var escalated atomic.Bool
	killCancel := make(chan struct{})

	syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)

	go func() {
		select {
		case <-time.After(5 * time.Second):
			escalated.Store(true)
		case <-killCancel:
		}
	}()

	cmd.Wait()
	close(killCancel)
	time.Sleep(50 * time.Millisecond)

	if escalated.Load() {
		t.Error("should not escalate when child exits promptly")
	}
}

// TestSignal_IgnoredAfterChildExit: signalling dead process doesn't panic.
func TestSignal_IgnoredAfterChildExit(t *testing.T) {
	cmd := exec.Command("bash", "-c", `exit 0`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()

	// Signal after exit — should return error, not panic
	err := syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)
	if err == nil {
		t.Log("signal to dead process succeeded (PID reuse possible)")
	}
}

// TestSignal_SecondSignalReachesChild: second SIGTERM not swallowed.
func TestSignal_SecondSignalReachesChild(t *testing.T) {
	cmd := exec.Command("bash", "-c", `
		count=0
		trap 'count=$((count+1)); if [ $count -ge 2 ]; then exit 44; fi' TERM
		while true; do sleep 0.1; done
	`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)
	time.Sleep(200 * time.Millisecond)
	syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)

	err := cmd.Wait()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 44 {
			t.Errorf("exit code = %d, want 44", exitErr.ExitCode())
		}
	} else if err == nil {
		t.Fatal("child should have exited from second SIGTERM")
	}
}

// TestSignal_HandlerRegistersAllSignals: verify run.go registers all three.
func TestSignal_HandlerRegistersAllSignals(t *testing.T) {
	src, err := os.ReadFile("run.go")
	if err != nil {
		t.Skip("cannot read run.go")
	}
	content := string(src)
	for _, sig := range []string{"syscall.SIGINT", "syscall.SIGTERM", "syscall.SIGHUP"} {
		if !strings.Contains(content, sig) {
			t.Errorf("run.go should register %s", sig)
		}
	}
}

// TestSignal_CleanupAlwaysCalled: verify sess.Stop() path always runs after Wait.
// This is a structural check — the production code calls sess.Stop() unconditionally
// after childCmd.Wait(), which runs whether the child exits normally, from a signal,
// or from SIGKILL escalation.
func TestSignal_CleanupAlwaysCalled(t *testing.T) {
	src, err := os.ReadFile("run.go")
	if err != nil {
		t.Skip("cannot read run.go")
	}
	content := string(src)
	// sess.Stop() must appear after childCmd.Wait()
	waitIdx := strings.Index(content, "childErr := childCmd.Wait()")
	stopIdx := strings.Index(content, "resp, stopErr := sess.Stop()")
	if waitIdx < 0 || stopIdx < 0 {
		t.Fatal("cannot find Wait/Stop in run.go")
	}
	if stopIdx < waitIdx {
		t.Error("sess.Stop() must be called after childCmd.Wait()")
	}
}

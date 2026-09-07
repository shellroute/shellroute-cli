//go:build !windows

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// All tests drive the production SignalHandler. Signals are sent to the test
// process itself and reach the handler through signal.Notify, as in production.

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

// childHandler is a running child with a handler attached to its process group.
type childHandler struct {
	cmd     *exec.Cmd
	h       *SignalHandler
	running atomic.Bool
	cancel  chan struct{}
	done    chan error // child's Wait result
}

func newHandler(sigs ...syscall.Signal) (*SignalHandler, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	var osSigs []os.Signal
	for _, s := range sigs {
		osSigs = append(osSigs, s)
	}
	return NewSignalHandler(cancel, osSigs...), ctx
}

func attachHandler(t *testing.T, script string, escalateAfter time.Duration) *childHandler {
	t.Helper()
	c := &childHandler{cmd: startChild(t, script), cancel: make(chan struct{}), done: make(chan error, 1)}
	c.running.Store(true)
	c.h, _ = newHandler(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	c.h.escalateAfter = escalateAfter
	c.h.Attach(-c.cmd.Process.Pid, &c.running, c.cancel)
	go func() { c.done <- c.cmd.Wait() }()
	return c
}

// wait returns the child's exit error once it is gone and the handler is stopped.
func (c *childHandler) wait(t *testing.T) error {
	t.Helper()
	var err error
	select {
	case err = <-c.done:
	case <-time.After(5 * time.Second):
		syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
		<-c.done
		t.Error("child still running after 5s")
	}
	c.running.Store(false)
	close(c.cancel)
	c.h.Stop()
	return err
}

func exitStatus(t *testing.T, err error) (code int, sig syscall.Signal) {
	t.Helper()
	if err == nil {
		return 0, 0
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("unexpected error: %v", err)
	}
	ws := ee.Sys().(syscall.WaitStatus)
	if ws.Signaled() {
		return -1, ws.Signal()
	}
	return ws.ExitStatus(), 0
}

func TestHandler_ForwardSIGTERM(t *testing.T) {
	c := attachHandler(t, `trap 'exit 42' TERM; while true; do sleep 0.1; done`, 5*time.Second)
	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	if code, _ := exitStatus(t, c.wait(t)); code != 42 {
		t.Errorf("exit code = %d, want 42", code)
	}
}

func TestHandler_ForwardSIGHUP(t *testing.T) {
	c := attachHandler(t, `trap 'exit 43' HUP; while true; do sleep 0.1; done`, 5*time.Second)
	syscall.Kill(syscall.Getpid(), syscall.SIGHUP)
	if code, _ := exitStatus(t, c.wait(t)); code != 43 {
		t.Errorf("exit code = %d, want 43", code)
	}
}

// Child ignores SIGTERM: handler escalates to SIGKILL after escalateAfter.
func TestHandler_EscalateToKILL(t *testing.T) {
	c := attachHandler(t, `trap '' TERM; while true; do sleep 0.1; done`, 500*time.Millisecond)
	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	if _, sig := exitStatus(t, c.wait(t)); sig != syscall.SIGKILL {
		t.Errorf("signal = %v, want SIGKILL", sig)
	}
}

// A second signal during the escalation wait kills immediately.
func TestHandler_SecondSignalImmediateKILL(t *testing.T) {
	c := attachHandler(t, `trap '' TERM INT; while true; do sleep 0.1; done`, 30*time.Second)
	start := time.Now()
	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	time.Sleep(200 * time.Millisecond)
	syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	_, sig := exitStatus(t, c.wait(t))
	if sig != syscall.SIGKILL {
		t.Errorf("signal = %v, want SIGKILL", sig)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v — second signal should have killed immediately", elapsed)
	}
}

// Child exits promptly: no SIGKILL, exit code preserved.
func TestHandler_NoEscalateIfChildExits(t *testing.T) {
	c := attachHandler(t, `trap 'exit 0' TERM; while true; do sleep 0.1; done`, 300*time.Millisecond)
	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	if code, sig := exitStatus(t, c.wait(t)); code != 0 || sig != 0 {
		t.Errorf("exit = (%d, %v), want clean exit 0", code, sig)
	}
}

// A signal after the child is gone must not touch its (possibly reused) pgid.
func TestHandler_SignalAfterChildExitIgnored(t *testing.T) {
	c := attachHandler(t, `exit 0`, 5*time.Second)
	<-c.done
	c.running.Store(false)
	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	time.Sleep(100 * time.Millisecond) // still registered: the handler must swallow it
	close(c.cancel)
	c.h.Stop()
}

func TestHandler_StopWithoutSignal(t *testing.T) {
	h, ctx := newHandler(syscall.SIGTERM)
	h.Stop()
	if ctx.Err() != nil {
		t.Error("startup cancelled without a signal")
	}
}

// Before Attach, the first signal cancels startup and is remembered.
func TestHandler_StartupSignalCancels(t *testing.T) {
	h, ctx := newHandler(syscall.SIGINT, syscall.SIGTERM)
	defer h.Stop()

	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("startup not cancelled after SIGTERM")
	}
	if sig := h.Wait(); sig != syscall.SIGTERM {
		t.Errorf("Wait() = %v, want SIGTERM", sig)
	}

	syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	time.Sleep(100 * time.Millisecond)
	if sig := h.StartupSignal(); sig != syscall.SIGTERM {
		t.Errorf("StartupSignal() = %v after second signal, want SIGTERM", sig)
	}
}

// A signal that arrived before Attach is forwarded to the child on Attach.
func TestHandler_AttachForwardsStartupSignal(t *testing.T) {
	h, ctx := newHandler(syscall.SIGTERM)
	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("startup not cancelled after SIGTERM")
	}

	c := &childHandler{h: h, cmd: startChild(t, `trap 'exit 42' TERM; while true; do sleep 0.1; done`), cancel: make(chan struct{}), done: make(chan error, 1)}
	c.running.Store(true)
	go func() { c.done <- c.cmd.Wait() }()
	h.Attach(-c.cmd.Process.Pid, &c.running, c.cancel)

	if code, _ := exitStatus(t, c.wait(t)); code != 42 {
		t.Errorf("exit code = %d, want 42 (startup signal forwarded)", code)
	}
}

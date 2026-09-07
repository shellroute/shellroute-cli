//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

// SignalHandlerConfig holds the dependencies for the child signal handler.
type SignalHandlerConfig struct {
	Pid           int           // child process PID (positive = process, negative = group)
	ChildRunning  *atomic.Bool  // set to false when child exits
	KillCancel    chan struct{} // closed when child exits (cancels escalation)
	EscalateAfter time.Duration // duration before SIGKILL escalation (default 5s)
}

// RunSignalHandler listens for SIGINT/SIGTERM/SIGHUP, forwards to the child
// process group, and escalates to SIGKILL if the child doesn't exit in time.
// A second signal during the escalation wait triggers immediate SIGKILL.
// Returns a cleanup function that must be called after the child exits.
func RunSignalHandler(cfg SignalHandlerConfig) func() {
	if cfg.EscalateAfter == 0 {
		cfg.EscalateAfter = 5 * time.Second
	}

	sigCh := make(chan os.Signal, 2) // buffer 2 so second signal isn't lost
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		sig, ok := <-sigCh
		if !ok || !cfg.ChildRunning.Load() {
			return
		}

		// Forward first signal to child process group
		syscall.Kill(cfg.Pid, sig.(syscall.Signal))

		// Wait for child exit, escalation timeout, or second signal
		select {
		case <-time.After(cfg.EscalateAfter):
			if cfg.ChildRunning.Load() {
				syscall.Kill(cfg.Pid, syscall.SIGKILL)
			}
		case <-sigCh:
			// Second signal — immediate SIGKILL
			if cfg.ChildRunning.Load() {
				syscall.Kill(cfg.Pid, syscall.SIGKILL)
			}
		case <-cfg.KillCancel:
			// Child exited normally
		}
	}()

	return func() {
		signal.Stop(sigCh)
		close(sigCh)
	}
}

// childSignal returns the signal that terminated the child, or 0.
func childSignal(err *exec.ExitError) syscall.Signal {
	if ws, ok := err.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal()
	}
	return 0
}

// exitAsChild ends the process the way the child ended: same exit code, or
// the same signal, so shells and systemd see the real cause.
func exitAsChild(err *exec.ExitError) {
	if sig := childSignal(err); sig != 0 {
		exitFromSignal(sig)
	}
	os.Exit(err.ExitCode())
}

// exitFromSignal re-raises sig on this process. Only stop signals and SIGKILL
// are re-raised; the Go runtime turns others (SIGSEGV, SIGQUIT, ...) into a
// crash dump. Falls back to the 128+n shell convention when sig is ignored.
func exitFromSignal(sig syscall.Signal) {
	switch sig {
	case syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGKILL:
		syscall.Kill(os.Getpid(), sig)
		time.Sleep(250 * time.Millisecond)
	}
	os.Exit(128 + int(sig))
}

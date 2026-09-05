//go:build !windows

package cli

import (
	"os"
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

//go:build !windows

package cli

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// SignalHandler owns stop-signal handling for the life of one session.
//
// Before Attach, a stop signal cancels session startup. After Attach, the
// first signal is forwarded to the child process group and escalated to
// SIGKILL if the child does not exit in time; a second signal during that
// wait kills immediately.
type SignalHandler struct {
	ch            chan os.Signal
	cancelStartup context.CancelFunc
	escalateAfter time.Duration

	mu         sync.Mutex
	startupSig syscall.Signal // first signal before Attach, 0 if none
	fired      chan struct{}  // closed with startupSig
	attached   bool
	pid        int
	running    *atomic.Bool
	killCancel chan struct{}
	stopOnce   sync.Once
}

// NewSignalHandler registers for sigs immediately, so no window exists
// between creating the session and starting the child. A signal ignored on
// entry (nohup, trap ” HUP) stays ignored: Notify would re-enable it and the
// child, which inherited the ignore, would end up SIGKILLed after the escalation.
func NewSignalHandler(cancelStartup context.CancelFunc, sigs ...os.Signal) *SignalHandler {
	h := &SignalHandler{
		ch:            make(chan os.Signal, 2), // room for a second signal during escalation
		cancelStartup: cancelStartup,
		escalateAfter: 5 * time.Second,
		fired:         make(chan struct{}),
	}
	var wanted []os.Signal
	for _, s := range sigs {
		if !signal.Ignored(s) {
			wanted = append(wanted, s)
		}
	}
	if len(wanted) > 0 { // Notify with no signals would relay every signal
		signal.Notify(h.ch, wanted...)
	}
	go h.loop()
	return h
}

func (h *SignalHandler) loop() {
	for s := range h.ch {
		sig := s.(syscall.Signal)
		h.mu.Lock()
		if !h.attached {
			if h.startupSig == 0 {
				h.startupSig = sig
				close(h.fired)
				h.cancelStartup()
			}
			h.mu.Unlock()
			continue
		}
		pid, running, killCancel := h.pid, h.running, h.killCancel
		h.mu.Unlock()

		if !running.Load() {
			continue
		}
		syscall.Kill(pid, sig)
		select {
		case <-time.After(h.escalateAfter):
			if running.Load() {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		case <-h.ch: // second signal
			if running.Load() {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		case <-killCancel:
		}
	}
}

// StartupSignal returns the signal received before Attach, or 0.
func (h *SignalHandler) StartupSignal() syscall.Signal {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.startupSig
}

// Wait blocks until a signal arrives; for modes that run no child.
func (h *SignalHandler) Wait() syscall.Signal {
	<-h.fired
	return h.StartupSignal()
}

// Attach starts forwarding signals to pid (negative = process group).
// A signal that arrived before Attach is forwarded now.
func (h *SignalHandler) Attach(pid int, running *atomic.Bool, killCancel chan struct{}) {
	h.mu.Lock()
	h.attached = true
	h.pid, h.running, h.killCancel = pid, running, killCancel
	sig := h.startupSig
	h.mu.Unlock()
	if sig != 0 {
		select {
		case h.ch <- sig:
		default: // buffer full: the pending signals get forwarded instead
		}
	}
}

// Stop unregisters the handler; later signals get default handling.
func (h *SignalHandler) Stop() {
	h.stopOnce.Do(func() {
		signal.Stop(h.ch)
		close(h.ch)
	})
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

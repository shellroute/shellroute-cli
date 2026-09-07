package session

import (
	"context"
	"testing"
	"time"
)

// Nothing listens on port 1, so detection can only end by timeout or cancel.

func TestDetectExitIPRetry_CancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if ip := detectExitIPRetry(ctx, 1, 30*time.Second); ip != "" {
		t.Fatalf("ip = %q, want empty", ip)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v, want immediate return on cancelled context", d)
	}
}

func TestDetectExitIPRetry_CancelledWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if ip := detectExitIPRetry(ctx, 1, 30*time.Second); ip != "" {
		t.Fatalf("ip = %q, want empty", ip)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v, want return shortly after cancel", d)
	}
}

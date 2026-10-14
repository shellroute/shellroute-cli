package proxy

import (
	"bufio"
	"context"
	"io"
	"net"
	"time"

	"github.com/shellroute/shellroute-cli/internal/meter"
)

// relay copies data bidirectionally between two connections, metering bytes.
func (s *Server) relay(ctx context.Context, client, remote net.Conn) {
	done := make(chan struct{}, 2)

	// Idle timeout: if no data flows in either direction for 60 seconds,
	// close both sides. Without this, a dead tunnel leaves the relay stuck
	// indefinitely (especially over TLS where half-close doesn't propagate
	// cleanly).
	idleTimeout := 60 * time.Second
	timer := time.AfterFunc(idleTimeout, func() {
		client.Close()
		remote.Close()
	})
	extend := func() {
		timer.Reset(idleTimeout)
	}

	go func() {
		copyWithIdleReset(&meteredWriter{remote, s.meter, true}, client, extend)
		if tc, ok := remote.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()

	go func() {
		copyWithIdleReset(&meteredWriter{client, s.meter, false}, remote, extend)
		if tc, ok := client.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()

	select {
	case <-done:
		timer.Stop()
	case <-ctx.Done():
		timer.Stop()
	}
}

// copyWithIdleReset copies from src to dst, calling onData after each chunk.
func copyWithIdleReset(dst io.Writer, src io.Reader, onData func()) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			onData()
			if _, wErr := dst.Write(buf[:n]); wErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// meteredWriter wraps a writer and counts bytes.
type meteredWriter struct {
	w     io.Writer
	meter *meter.Meter
	isUp  bool
}

func (mw *meteredWriter) Write(p []byte) (int, error) {
	n, err := mw.w.Write(p)
	if n > 0 {
		if mw.isUp {
			mw.meter.AddUp(int64(n))
		} else {
			mw.meter.AddDown(int64(n))
		}
	}
	return n, err
}

// bufferedConn wraps a net.Conn with a bufio.Reader, so that bytes already
// buffered from HTTP response parsing are not lost during relay.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (bc *bufferedConn) Read(p []byte) (int, error) {
	return bc.r.Read(p)
}

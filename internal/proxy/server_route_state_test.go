package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shellroute/shellroute-cli/internal/meter"
)

func newTestServer(t *testing.T, gatewayAddr string) *Server {
	t.Helper()
	s := NewServer(&Config{
		ListenPort:  0,
		GatewayAddr: gatewayAddr,
		GatewayTLS:  false,
		Token:       "test-token",
		Meter:       meter.New(),
		ByteBudget:  -1,
	})
	return s
}

// --- Route state tests ---

func TestRouteStateTransitions(t *testing.T) {
	s := newTestServer(t, "localhost:0")

	// Start healthy
	if s.RouteState() != RouteHealthy {
		t.Errorf("initial state = %d, want RouteHealthy", s.RouteState())
	}

	// healthy → dead
	s.SetRouteDead(false)
	if s.RouteState() != RouteDead {
		t.Errorf("state after SetRouteDead = %d, want RouteDead", s.RouteState())
	}

	// dead → rotating
	s.SetRouteRotating()
	if s.RouteState() != RouteRotating {
		t.Errorf("state after SetRouteRotating = %d, want RouteRotating", s.RouteState())
	}

	// rotating → healthy
	s.SetRouteHealthy()
	if s.RouteState() != RouteHealthy {
		t.Errorf("state after SetRouteHealthy = %d, want RouteHealthy", s.RouteState())
	}

	// healthy → depleted
	s.SetRouteDepleted()
	if s.RouteState() != RouteDepleted {
		t.Errorf("state after SetRouteDepleted = %d, want RouteDepleted", s.RouteState())
	}
}

func TestCheckRouteStateRejectsDead(t *testing.T) {
	s := newTestServer(t, "localhost:0")
	s.SetRouteDead(false)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// Read response in background (pipe has no buffer)
	done := make(chan int)
	go func() {
		resp, err := http.ReadResponse(bufio.NewReader(client), nil)
		if err != nil {
			done <- 0
			return
		}
		done <- resp.StatusCode
	}()

	rejected := s.checkRouteState(server)
	if !rejected {
		t.Error("should reject when dead")
	}

	status := <-done
	if status != 503 {
		t.Errorf("status = %d, want 503", status)
	}
}

func TestCheckRouteStateRejectsRotating(t *testing.T) {
	s := newTestServer(t, "localhost:0")
	s.SetRouteRotating()

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() { http.ReadResponse(bufio.NewReader(client), nil) }()

	rejected := s.checkRouteState(server)
	if !rejected {
		t.Error("should reject when rotating")
	}
}

func TestCheckRouteStateRejectsDepleted(t *testing.T) {
	s := newTestServer(t, "localhost:0")
	s.SetRouteDepleted()

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan int)
	go func() {
		resp, err := http.ReadResponse(bufio.NewReader(client), nil)
		if err != nil {
			done <- 0
			return
		}
		done <- resp.StatusCode
	}()

	rejected := s.checkRouteState(server)
	if !rejected {
		t.Error("should reject when depleted")
	}

	status := <-done
	if status != 402 {
		t.Errorf("status = %d, want 402 for depleted", status)
	}
}

func TestCheckRouteStateAllowsHealthy(t *testing.T) {
	s := newTestServer(t, "localhost:0")

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	rejected := s.checkRouteState(server)
	if rejected {
		t.Error("should allow when healthy")
	}
}

func TestSwapUpstream(t *testing.T) {
	s := newTestServer(t, "old-gateway:8080")
	s.SetRouteDead(false) // simulate dead state

	s.SwapUpstream("new-gateway:8080", false, "new-token")

	// Should be healthy after swap
	if s.RouteState() != RouteHealthy {
		t.Errorf("state after SwapUpstream = %d, want RouteHealthy", s.RouteState())
	}

	// Gateway addr should be updated
	s.mu.Lock()
	addr := s.gatewayAddr
	s.mu.Unlock()
	if addr != "new-gateway:8080" {
		t.Errorf("gatewayAddr = %q, want new-gateway:8080", addr)
	}
}

// --- Active connection tracking tests ---

func TestTrackedConnsCloseOnDead(t *testing.T) {
	s := NewServer(&Config{
		ListenPort:  0,
		GatewayAddr: "localhost:0",
		Token:       "test",
		Meter:       meter.New(),
		ByteBudget:  -1,
		TrackRelays: true,
	})

	// Create mock connections and track them
	c1, s1 := net.Pipe()
	c2, s2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	s.trackConn(s1)
	s.trackConn(s2)

	s.trackedMu.Lock()
	count := len(s.trackedConns)
	s.trackedMu.Unlock()
	if count != 2 {
		t.Errorf("tracked = %d, want 2", count)
	}

	// SetRouteDead(true) should close tracked connections
	s.SetRouteDead(true)

	// Try reading from the server-side pipes — should get error (closed)
	buf := make([]byte, 1)
	_, err := s1.Read(buf)
	if err == nil {
		t.Error("s1 should be closed after SetRouteDead(true)")
	}
	_, err = s2.Read(buf)
	if err == nil {
		t.Error("s2 should be closed after SetRouteDead(true)")
	}
}

func TestTrackedConnsNotClosedOnDeadFalse(t *testing.T) {
	s := NewServer(&Config{
		ListenPort:  0,
		GatewayAddr: "localhost:0",
		Token:       "test",
		Meter:       meter.New(),
		ByteBudget:  -1,
		TrackRelays: true,
	})

	c1, s1 := net.Pipe()
	defer c1.Close()
	defer s1.Close()

	s.trackConn(s1)

	// SetRouteDead(false) should NOT close tracked connections (interactive mode)
	s.SetRouteDead(false)

	s.trackedMu.Lock()
	count := len(s.trackedConns)
	s.trackedMu.Unlock()
	// Connection should still be tracked (not closed)
	if count != 1 {
		t.Errorf("tracked = %d, want 1 (should not close in interactive mode)", count)
	}
}

func TestTrackUntrack(t *testing.T) {
	s := NewServer(&Config{
		ListenPort:  0,
		GatewayAddr: "localhost:0",
		Token:       "test",
		Meter:       meter.New(),
		ByteBudget:  -1,
		TrackRelays: true,
	})

	c1, s1 := net.Pipe()
	defer c1.Close()
	defer s1.Close()

	s.trackConn(s1)
	s.trackedMu.Lock()
	if len(s.trackedConns) != 1 {
		t.Error("should have 1 tracked conn")
	}
	s.trackedMu.Unlock()

	s.untrackConn(s1)
	s.trackedMu.Lock()
	if len(s.trackedConns) != 0 {
		t.Error("should have 0 tracked conns after untrack")
	}
	s.trackedMu.Unlock()
}

func TestNoTrackingWhenDisabled(t *testing.T) {
	s := NewServer(&Config{
		ListenPort:  0,
		GatewayAddr: "localhost:0",
		Token:       "test",
		Meter:       meter.New(),
		ByteBudget:  -1,
		TrackRelays: false, // disabled
	})

	c1, s1 := net.Pipe()
	defer c1.Close()
	defer s1.Close()

	s.trackConn(s1) // should be no-op
	s.trackedMu.Lock()
	if len(s.trackedConns) != 0 {
		t.Error("should not track when disabled")
	}
	s.trackedMu.Unlock()
}

// --- Dead-before-502 test ---

func TestDeadStateSetBeforeClientResponse(t *testing.T) {
	// Create a "gateway" that immediately closes the connection
	gwLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gwLn.Close()
	go func() {
		for {
			conn, err := gwLn.Accept()
			if err != nil {
				return
			}
			conn.Close() // immediate close = gateway unreachable
		}
	}()

	s := newTestServer(t, gwLn.Addr().String())

	// Simulate a CONNECT attempt — gateway closes immediately
	client, server := net.Pipe()
	defer client.Close()

	go func() {
		result := s.dialAndSendConnect(server, "example.com:443")
		if result != nil {
			result.Close()
		}
		server.Close()
	}()

	// Read the response — should be 502 (transient, not RouteDead)
	resp, _ := http.ReadResponse(bufio.NewReader(client), nil)
	if resp != nil && resp.StatusCode != 502 {
		t.Errorf("expected 502, got %d", resp.StatusCode)
	}

	time.Sleep(50 * time.Millisecond)

	// Gateway dial failure should NOT mark route dead — it's transient.
	// Only X-Route-Dead from gateway sets RouteDead.
	if s.RouteState() != RouteHealthy {
		t.Errorf("route state = %d, want RouteHealthy (%d) — dial failure is transient, not RouteDead",
			s.RouteState(), RouteHealthy)
	}
}

func TestXRouteDeadHeaderSetsRouteDead(t *testing.T) {
	// Gateway returns X-Route-Dead: true → RouteDead must be set.
	// This is the ONLY gateway response that should permanently kill the route.
	gwLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gwLn.Close()
	go func() {
		for {
			conn, err := gwLn.Accept()
			if err != nil {
				return
			}
			// Read the CONNECT request, respond with X-Route-Dead
			buf := make([]byte, 4096)
			conn.Read(buf)
			conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nX-Route-Dead: true\r\n\r\n"))
			conn.Close()
		}
	}()

	s := newTestServer(t, gwLn.Addr().String())

	callbackCh := make(chan string, 1)
	s.OnUpstreamDead = func(reason string) {
		callbackCh <- reason
	}

	client, server := net.Pipe()
	defer client.Close()

	go func() {
		result := s.dialAndSendConnect(server, "example.com:443")
		if result != nil {
			result.Close()
		}
		server.Close()
	}()

	resp, _ := http.ReadResponse(bufio.NewReader(client), nil)
	if resp != nil && resp.StatusCode != 502 {
		t.Errorf("expected 502, got %d", resp.StatusCode)
	}

	select {
	case reason := <-callbackCh:
		if reason == "" {
			t.Error("callback reason should not be empty")
		}
	case <-time.After(time.Second):
		t.Error("OnUpstreamDead callback should have been called")
	}

	if s.RouteState() != RouteDead {
		t.Errorf("route state = %d, want RouteDead (%d) — X-Route-Dead must set RouteDead",
			s.RouteState(), RouteDead)
	}
}

func TestDialFailureDoesNotPoisonRoute(t *testing.T) {
	// After a transient gateway dial failure, subsequent requests
	// should still be attempted (route stays healthy).
	gwLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gwLn.Close() // gateway "down"

	s := newTestServer(t, gwLn.Addr().String())

	// First request — dial fails, returns 502
	c1, s1 := net.Pipe()
	go func() {
		s.dialAndSendConnect(s1, "example.com:443")
		s1.Close()
	}()
	resp, _ := http.ReadResponse(bufio.NewReader(c1), nil)
	c1.Close()
	if resp == nil || resp.StatusCode != 502 {
		t.Fatalf("expected 502 on first request")
	}

	// Route should still be healthy — not poisoned
	if s.RouteState() != RouteHealthy {
		t.Fatalf("route state = %d after dial failure, want RouteHealthy — transient failure must not poison route",
			s.RouteState())
	}

	// checkRouteState should allow the next request
	c2, s2 := net.Pipe()
	defer c2.Close()
	defer s2.Close()
	if s.checkRouteState(s2) {
		t.Error("checkRouteState should pass — route is still healthy after transient failure")
	}
}

func TestBufferedConnReadsBufferedData(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	// Write data to c1
	go func() {
		c1.Write([]byte("hello world"))
		c1.Close()
	}()

	// Wrap c2 in buffered reader, read some data into buffer
	br := bufio.NewReader(c2)
	peeked, err := br.Peek(5) // "hello" buffered
	if err != nil {
		t.Fatal(err)
	}
	if string(peeked) != "hello" {
		t.Errorf("peek = %q, want hello", peeked)
	}

	// Wrap in bufferedConn
	bc := &bufferedConn{Conn: c2, r: br}

	// Read should get the buffered data first, then the rest
	all := make([]byte, 20)
	n, _ := bc.Read(all)
	result := string(all[:n])
	if !strings.HasPrefix(result, "hello") {
		t.Errorf("bufferedConn read = %q, should start with 'hello'", result)
	}
}

func TestRelayNormalCloseDoesNotTriggerDeath(t *testing.T) {
	s := newTestServer(t, "localhost:0")

	var deadCalled bool
	s.OnUpstreamDead = func(reason string) {
		deadCalled = true
	}

	// Create two connected pipe pairs to simulate client↔remote relay
	clientRead, _ := net.Pipe()
	remoteRead, remoteWrite := net.Pipe()
	defer clientRead.Close()
	defer remoteWrite.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		s.relay(ctx, clientRead, remoteRead)
		close(done)
	}()

	// Simulate normal traffic: remote sends data, then both sides close (normal EOF)
	remoteWrite.Write([]byte("hello from remote"))
	remoteWrite.Close() // remote done
	clientRead.Close()  // client done — both relay directions finish

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay should complete after both sides close")
	}

	// OnUpstreamDead should NOT have been called — relay close is normal
	if deadCalled {
		t.Error("OnUpstreamDead should NOT fire on normal relay close")
	}

	// Route state should still be healthy
	if s.RouteState() != RouteHealthy {
		t.Errorf("route state = %d, want RouteHealthy after normal relay close", s.RouteState())
	}
}

func TestRelayNormalCloseDoesNotTriggerDeathRunMode(t *testing.T) {
	s := NewServer(&Config{
		ListenPort:  0,
		GatewayAddr: "localhost:0",
		Token:       "test",
		Meter:       meter.New(),
		ByteBudget:  -1,
		TrackRelays: true, // run mode
	})

	var deadCalled bool
	s.OnUpstreamDead = func(reason string) {
		deadCalled = true
	}

	clientRead, _ := net.Pipe()
	remoteRead, remoteWrite := net.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.trackConn(remoteRead)

	done := make(chan struct{})
	go func() {
		s.relay(ctx, clientRead, remoteRead)
		close(done)
	}()

	// Send data then close both sides normally
	remoteWrite.Write([]byte("response data"))
	remoteWrite.Close()
	clientRead.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay should complete after both sides close")
	}

	s.untrackConn(remoteRead)

	// Even in run mode, normal relay close should NOT trigger death
	if deadCalled {
		t.Error("OnUpstreamDead should NOT fire on normal relay close (even in run mode)")
	}
	if s.RouteState() != RouteHealthy {
		t.Errorf("route state should stay healthy after normal relay close")
	}
}

func TestCheckBudgetSetsDepletedBeforeResponse(t *testing.T) {
	s := NewServer(&Config{
		ListenPort:  0,
		GatewayAddr: "localhost:0",
		Token:       "test",
		Meter:       meter.New(),
		ByteBudget:  100, // very small budget
	})

	// Add bytes to exceed budget
	s.meter.AddUp(200)

	var callbackState int32 = -1
	s.OnUpstreamDead = func(reason string) {
		callbackState = s.RouteState()
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		// drain the response
		buf := make([]byte, 1024)
		client.Read(buf)
	}()

	exceeded := s.checkBudget(server)
	if !exceeded {
		t.Error("should exceed budget")
	}

	time.Sleep(10 * time.Millisecond) // let callback run

	if callbackState != RouteDepleted {
		t.Errorf("route state should be RouteDepleted in callback, got %d", callbackState)
	}
}

func TestCopyWithIdleReset_CallsOnData(t *testing.T) {
	r, w := io.Pipe()
	var buf bytes.Buffer
	var calls atomic.Int32

	go func() {
		w.Write([]byte("hello"))
		w.Write([]byte("world"))
		w.Close()
	}()

	copyWithIdleReset(&buf, r, func() { calls.Add(1) })

	if buf.String() != "helloworld" {
		t.Errorf("got %q, want helloworld", buf.String())
	}
	if calls.Load() < 2 {
		t.Errorf("onData called %d times, want >=2", calls.Load())
	}
}

func TestRelayIdleTimeout_ClosesOnStall(t *testing.T) {
	// Simulate a stalled remote: client sends data, remote never responds.
	// The relay's idle timeout should close both sides.
	gwLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gwLn.Close()

	// Gateway accepts but never sends data (simulates dead tunnel)
	go func() {
		conn, err := gwLn.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 4096)
		conn.Read(buf)
		conn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
		// Stall — never send more data, never close
		time.Sleep(10 * time.Second)
		conn.Close()
	}()

	s := newTestServer(t, gwLn.Addr().String())

	client, server := net.Pipe()
	defer client.Close()

	relayDone := make(chan struct{})
	go func() {
		s.handleConnect(context.Background(), server, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com\r\nProxy-Authorization: Basic "+s.gatewayAuth+"\r\n\r\n")
		server.Close()
		close(relayDone)
	}()

	// Read the 200 response
	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil || resp.StatusCode != 200 {
		t.Skipf("gateway didn't accept CONNECT (status=%v err=%v)", resp, err)
	}

	// Now the relay is running. The remote side is stalled.
	// The 60s idle timeout should close everything. But we don't want to wait
	// 60s in a test — just verify the relay eventually completes (not stuck forever).
	select {
	case <-relayDone:
		// success — relay exited (either timeout or close propagation)
	case <-time.After(90 * time.Second):
		t.Fatal("relay did not exit after idle timeout — stuck forever (the bug we fixed)")
	}
}

func TestCopyWithIdleReset_PropagatesWriteError(t *testing.T) {
	data := []byte("test data")
	src := bytes.NewReader(data)
	// Writer that always fails
	dst := &failWriter{}

	copyWithIdleReset(dst, src, func() {})
	// Should return without hanging — write error terminates copy
}

type failWriter struct{}

func (f *failWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write failed") }

func init() {
	_ = fmt.Sprintf
	_ = strings.HasPrefix
}

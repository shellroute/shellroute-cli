package proxy

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shellroute/shellroute-cli/internal/meter"
)

// fakeGateway starts a TCP listener that responds to CONNECT with a custom response.
func fakeGateway(t *testing.T, response string) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				n, err := c.Read(buf)
				if err != nil || n == 0 {
					return
				}
				// Only respond to CONNECT requests
				if strings.HasPrefix(string(buf[:n]), "CONNECT") {
					c.Write([]byte(response))
					// Keep connection open briefly for relay
					time.Sleep(100 * time.Millisecond)
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func TestDialAndSendConnect_ParsesExitIPHeader(t *testing.T) {
	gwAddr, cleanup := fakeGateway(t, "HTTP/1.1 200 OK\r\nX-Exit-IP: 5.6.7.8\r\n\r\n")
	defer cleanup()

	var got string
	var mu sync.Mutex
	s := &Server{
		gatewayAddr: gwAddr,

		OnExitIPChanged: func(ip string) {
			mu.Lock()
			got = ip
			mu.Unlock()
		},
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	result := s.dialAndSendConnect(server, "example.com:443")
	if result == nil {
		t.Fatal("dialAndSendConnect returned nil — gateway should have returned 200")
	}
	result.Close()

	mu.Lock()
	defer mu.Unlock()
	if got != "5.6.7.8" {
		t.Errorf("OnExitIPChanged got %q, want 5.6.7.8", got)
	}
}

func TestDialAndSendConnect_NoHeaderNoCallback(t *testing.T) {
	gwAddr, cleanup := fakeGateway(t, "HTTP/1.1 200 OK\r\n\r\n")
	defer cleanup()

	called := false
	s := &Server{
		gatewayAddr: gwAddr,

		OnExitIPChanged: func(ip string) {
			called = true
		},
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	result := s.dialAndSendConnect(server, "example.com:443")
	if result == nil {
		t.Fatal("dialAndSendConnect returned nil")
	}
	result.Close()

	if called {
		t.Error("OnExitIPChanged should not be called when no X-Exit-IP header")
	}
}

func TestDialAndSendConnect_IPChangeMidSession(t *testing.T) {
	// Simulate gateway returning different IPs on consecutive connects
	var mu sync.Mutex
	ips := []string{"1.1.1.1", "2.2.2.2"}
	callIdx := 0

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				n, _ := c.Read(buf)
				if n == 0 {
					return
				}
				mu.Lock()
				ip := ips[callIdx%len(ips)]
				callIdx++
				mu.Unlock()
				c.Write([]byte("HTTP/1.1 200 OK\r\nX-Exit-IP: " + ip + "\r\n\r\n"))
				time.Sleep(100 * time.Millisecond)
			}(conn)
		}
	}()

	var receivedIPs []string
	var ipMu sync.Mutex
	s := &Server{
		gatewayAddr: ln.Addr().String(),

		OnExitIPChanged: func(ip string) {
			ipMu.Lock()
			receivedIPs = append(receivedIPs, ip)
			ipMu.Unlock()
		},
	}

	// First connect
	c1, s1 := net.Pipe()
	defer c1.Close()
	r1 := s.dialAndSendConnect(s1, "example.com:443")
	s1.Close()
	if r1 != nil {
		r1.Close()
	}

	// Second connect — different IP
	c2, s2 := net.Pipe()
	defer c2.Close()
	r2 := s.dialAndSendConnect(s2, "example.com:443")
	s2.Close()
	if r2 != nil {
		r2.Close()
	}

	ipMu.Lock()
	defer ipMu.Unlock()
	if len(receivedIPs) != 2 {
		t.Fatalf("expected 2 IP callbacks, got %d: %v", len(receivedIPs), receivedIPs)
	}
	if receivedIPs[0] != "1.1.1.1" {
		t.Errorf("first IP = %q, want 1.1.1.1", receivedIPs[0])
	}
	if receivedIPs[1] != "2.2.2.2" {
		t.Errorf("second IP = %q, want 2.2.2.2", receivedIPs[1])
	}
}

func TestDialAndSendConnect_NilCallbackDoesNotPanic(t *testing.T) {
	gwAddr, cleanup := fakeGateway(t, "HTTP/1.1 200 OK\r\nX-Exit-IP: 9.9.9.9\r\n\r\n")
	defer cleanup()

	s := &Server{
		gatewayAddr: gwAddr,

		OnExitIPChanged: nil, // not set
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// Should not panic
	result := s.dialAndSendConnect(server, "example.com:443")
	if result != nil {
		result.Close()
	}
}

func TestDialAndSendConnect_NonOKDoesNotKillRoute(t *testing.T) {
	// Gateway returns 502 — should NOT trigger OnUpstreamDead
	gwAddr, cleanup := fakeGateway(t, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
	defer cleanup()

	deadCalled := false
	s := &Server{
		gatewayAddr: gwAddr,

		OnUpstreamDead: func(reason string) {
			deadCalled = true
		},
	}

	client, server := net.Pipe()
	defer client.Close()
	// Drain client side so writes to server don't block
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := client.Read(buf); err != nil {
				return
			}
		}
	}()

	result := s.dialAndSendConnect(server, "example.com:443")
	server.Close()
	if result != nil {
		t.Error("502 should return nil connection")
		result.Close()
	}
	if deadCalled {
		t.Error("502 from gateway should NOT trigger OnUpstreamDead")
	}
}

func TestDialAndSendConnect_402TriggersDepleted(t *testing.T) {
	gwAddr, cleanup := fakeGateway(t, "HTTP/1.1 402 Payment Required\r\n\r\nbalance_depleted")
	defer cleanup()

	var reason string
	s := &Server{
		gatewayAddr: gwAddr,

		OnUpstreamDead: func(r string) {
			reason = r
		},
	}

	client, server := net.Pipe()
	defer client.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := client.Read(buf); err != nil {
				return
			}
		}
	}()

	result := s.dialAndSendConnect(server, "example.com:443")
	server.Close()
	if result != nil {
		t.Error("402 should return nil")
		result.Close()
	}
	if reason != "depleted" {
		t.Errorf("402 should fire OnUpstreamDead with 'depleted', got %q", reason)
	}
}

func TestDialAndSendConnect_DialFailureDoesNotKillRoute(t *testing.T) {
	// Gateway unreachable — should NOT trigger OnUpstreamDead (transient)
	deadCalled := false
	s := &Server{
		gatewayAddr: "127.0.0.1:1", // nothing listening

		OnUpstreamDead: func(r string) {
			deadCalled = true
		},
	}

	client, server := net.Pipe()
	defer client.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := client.Read(buf); err != nil {
				return
			}
		}
	}()

	result := s.dialAndSendConnect(server, "example.com:443")
	server.Close()
	if result != nil {
		t.Error("dial failure should return nil")
		result.Close()
	}
	if deadCalled {
		t.Error("dial failure should NOT trigger OnUpstreamDead (transient)")
	}
}

// --- Budget / depletion tests ---

func TestSetBudget_UpdatesBudget(t *testing.T) {
	s := &Server{}
	s.budget.Store(100)
	s.SetBudget(999)
	if s.budget.Load() != 999 {
		t.Errorf("budget = %d, want 999", s.budget.Load())
	}
}

func TestBytesTotal_ReturnsMeterTotal(t *testing.T) {
	m := &meter.Meter{}
	s := &Server{meter: m}
	m.AddUp(100)
	m.AddDown(200)
	if s.BytesTotal() != 300 {
		t.Errorf("BytesTotal = %d, want 300", s.BytesTotal())
	}
}

func TestCheckBudget_ExceededTriggersDepletion(t *testing.T) {
	m := &meter.Meter{}
	m.AddDown(1000)
	var reason string
	s := &Server{
		meter:          m,
		OnUpstreamDead: func(r string) { reason = r },
	}
	s.budget.Store(500)

	client, server := net.Pipe()
	defer client.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := client.Read(buf); err != nil {
				return
			}
		}
	}()

	exceeded := s.checkBudget(server)
	server.Close()
	if !exceeded {
		t.Error("checkBudget should return true when over budget")
	}
	if reason != "depleted" {
		t.Errorf("expected depleted, got %q", reason)
	}
	if s.routeState.Load() != RouteDepleted {
		t.Error("routeState should be RouteDepleted")
	}
}

func TestCheckBudget_UnderBudgetAllows(t *testing.T) {
	m := &meter.Meter{}
	m.AddDown(100)
	s := &Server{
		meter: m,
	}
	s.budget.Store(500)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	exceeded := s.checkBudget(server)
	if exceeded {
		t.Error("checkBudget should return false when under budget")
	}
}

func TestCheckBudget_UnlimitedNeverExceeds(t *testing.T) {
	m := &meter.Meter{}
	m.AddDown(999999999)
	s := &Server{
		meter: m,
	}
	s.budget.Store(-1)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	exceeded := s.checkBudget(server)
	if exceeded {
		t.Error("unlimited budget should never exceed")
	}
}

func TestRouteState_DepletedBlocks(t *testing.T) {
	s := &Server{}
	s.routeState.Store(RouteDepleted)

	client, server := net.Pipe()
	defer client.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := client.Read(buf); err != nil {
				return
			}
		}
	}()

	blocked := s.checkRouteState(server)
	server.Close()
	if !blocked {
		t.Error("depleted route should block requests")
	}
}

func TestRouteState_HealthyAllows(t *testing.T) {
	s := &Server{}
	s.routeState.Store(RouteHealthy)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	blocked := s.checkRouteState(server)
	if blocked {
		t.Error("healthy route should not block")
	}
}

func TestSetRouteHealthy_ClearsDepleted(t *testing.T) {
	s := &Server{}
	s.routeState.Store(RouteDepleted)
	s.SetRouteHealthy()
	if s.routeState.Load() != RouteHealthy {
		t.Errorf("expected RouteHealthy, got %d", s.routeState.Load())
	}
}

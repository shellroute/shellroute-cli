package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shellroute/shellroute-cli/internal/meter"
)

// Route state constants for the local proxy health gate.
const (
	RouteHealthy  int32 = 0
	RouteDead     int32 = 1
	RouteRotating int32 = 2
	RouteDepleted int32 = 3
)

// Server is a local HTTP proxy that forwards traffic to a remote gateway.
type Server struct {
	listenAddr      string
	gatewayAddr     string
	gatewayTLS      bool   // true = tls.Dial, false = net.Dial
	gatewayAuth     string // base64-encoded "user:pass"
	meter           *meter.Meter
	listener        net.Listener
	mu              sync.Mutex
	activeConns     int
	OnUpstreamDead  func(reason string) // called on upstream failure
	OnExitIPChanged func(ip string)     // called when gateway reports a different exit IP
	OnRelayDone     func()              // called after each relay ends
	budget          atomic.Int64        // byte budget from API (-1 = unlimited)

	// Route health gate — checked before every CONNECT/HTTP
	routeState atomic.Int32 // RouteHealthy, RouteDead, RouteRotating, RouteDepleted

	// Tracked relay connections for run-mode force-close
	trackedMu    sync.Mutex
	trackedConns map[net.Conn]struct{}
	trackRelays  bool // true in run mode — track relay conns for force-close
}

// Config holds proxy server configuration.
type Config struct {
	ListenPort  int
	GatewayAddr string // host:port of the gateway
	GatewayTLS  bool   // use TLS when connecting to gateway
	Token       string // gateway session token
	Meter       *meter.Meter
	ByteBudget  int64 // max bytes allowed (-1 = unlimited, 0 = blocked)
	TrackRelays bool  // true = track active relay conns for force-close (run mode)
}

func NewServer(cfg *Config) *Server {
	auth := base64.StdEncoding.EncodeToString(
		[]byte("_:" + cfg.Token),
	)
	budget := cfg.ByteBudget
	if budget == 0 {
		budget = -1
	}
	s := &Server{
		listenAddr:   fmt.Sprintf("127.0.0.1:%d", cfg.ListenPort),
		gatewayAddr:  cfg.GatewayAddr,
		gatewayTLS:   cfg.GatewayTLS,
		gatewayAuth:  auth,
		meter:        cfg.Meter,
		trackRelays:  cfg.TrackRelays,
		trackedConns: make(map[net.Conn]struct{}),
	}
	s.budget.Store(budget)
	return s
}

// RouteState returns the current route state.
func (s *Server) RouteState() int32 {
	return s.routeState.Load()
}

// SetRouteState atomically sets the route state.
func (s *Server) SetRouteState(state int32) {
	s.routeState.Store(state)
}

// SetRouteDead marks the route as dead. If closeActive is true (run mode),
// closes all tracked relay connections immediately.
func (s *Server) SetRouteDead(closeActive bool) {
	s.routeState.Store(RouteDead)
	if closeActive {
		s.closeTrackedConns()
	}
}

// SetRouteDepleted marks the route as balance-depleted.
func (s *Server) SetRouteDepleted() {
	s.routeState.Store(RouteDepleted)
}

// SetRouteHealthy marks the route as healthy.
func (s *Server) SetRouteHealthy() {
	s.routeState.Store(RouteHealthy)
}

// SetBudget updates the byte budget (e.g. after user tops up).
func (s *Server) SetBudget(b int64) {
	s.budget.Store(b)
}

// Budget returns the current byte budget.
func (s *Server) Budget() int64 {
	return s.budget.Load()
}

// BytesTotal returns the cumulative bytes metered through this proxy.
func (s *Server) BytesTotal() int64 {
	return s.meter.BytesTotal()
}

// SetRouteRotating marks the route as rotating.
func (s *Server) SetRouteRotating() {
	s.routeState.Store(RouteRotating)
}

// SwapUpstream atomically replaces the gateway credentials for rotation.
// No listener gap — same port, new upstream. Resets route state to healthy.
func (s *Server) SwapUpstream(gatewayAddr string, gatewayTLS bool, token string) {
	auth := base64.StdEncoding.EncodeToString([]byte("_:" + token))
	s.mu.Lock()
	s.gatewayAddr = gatewayAddr
	s.gatewayTLS = gatewayTLS
	s.gatewayAuth = auth
	s.mu.Unlock()
	s.routeState.Store(RouteHealthy)
}

// closeTrackedConns closes all active relay connections (run mode force-close).
func (s *Server) closeTrackedConns() {
	s.trackedMu.Lock()
	for conn := range s.trackedConns {
		conn.Close()
	}
	s.trackedConns = make(map[net.Conn]struct{})
	s.trackedMu.Unlock()
}

func (s *Server) trackConn(conn net.Conn) {
	if !s.trackRelays {
		return
	}
	s.trackedMu.Lock()
	s.trackedConns[conn] = struct{}{}
	s.trackedMu.Unlock()
}

func (s *Server) untrackConn(conn net.Conn) {
	if !s.trackRelays {
		return
	}
	s.trackedMu.Lock()
	delete(s.trackedConns, conn)
	s.trackedMu.Unlock()
}

// Start begins accepting connections. Blocks until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.listenAddr, err)
	}
	s.listener = ln

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("accept error: %v", err)
			continue
		}
		go s.handleConn(ctx, conn)
	}
}

// Addr returns the listener address. Only valid after Start is called.
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.listenAddr
}

func (s *Server) ActiveConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeConns
}

// checkRouteState returns an error response if the route is not healthy.
// Returns true if the request should be rejected.
func (s *Server) checkRouteState(clientConn net.Conn) bool {
	state := s.routeState.Load()
	switch state {
	case RouteDead, RouteRotating:
		clientConn.Write([]byte("HTTP/1.1 503 Service Unavailable\r\n\r\n"))
		return true
	case RouteDepleted:
		clientConn.Write([]byte("HTTP/1.1 402 Payment Required\r\n\r\nbalance_depleted"))
		return true
	}
	return false
}

func (s *Server) handleConn(ctx context.Context, clientConn net.Conn) {
	s.mu.Lock()
	s.activeConns++
	s.mu.Unlock()

	defer func() {
		clientConn.Close()
		s.mu.Lock()
		s.activeConns--
		s.mu.Unlock()
	}()

	buf := make([]byte, 8192)
	n, err := clientConn.Read(buf)
	if err != nil {
		return
	}
	data := string(buf[:n])

	firstLine := data
	if idx := strings.Index(data, "\r\n"); idx != -1 {
		firstLine = data[:idx]
	}
	parts := strings.SplitN(firstLine, " ", 3)
	if len(parts) < 3 {
		clientConn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	method := parts[0]

	if method == http.MethodConnect {
		s.handleConnect(ctx, clientConn, data)
	} else {
		s.handleHTTP(ctx, clientConn, buf[:n])
	}
}

// checkBudget returns true if the local byte budget is exceeded.
func (s *Server) checkBudget(clientConn net.Conn) bool {
	b := s.budget.Load()
	if b > 0 && s.meter.BytesTotal() >= b {
		s.routeState.Store(RouteDepleted) // BEFORE response — prevents fast-retry race
		clientConn.Write([]byte("HTTP/1.1 402 Payment Required\r\n\r\nbalance_depleted"))
		s.upstreamDeadWithReason("depleted")
		return true
	}
	return false
}

// dialAndSendConnect dials the gateway and sends a CONNECT request for the target host.
// Returns the gateway connection on success, or nil + handles error response to client.
func (s *Server) dialAndSendConnect(clientConn net.Conn, targetHost string) net.Conn {
	s.mu.Lock()
	gwAddr := s.gatewayAddr
	gwTLS := s.gatewayTLS
	gwAuth := s.gatewayAuth
	s.mu.Unlock()

	gatewayConn, err := s.dialGatewayWith(gwAddr, gwTLS)
	if err != nil {
		// Gateway dial failed — return 502 but do NOT mark route dead.
		// The gateway is a Docker container and rarely goes down. Transient
		// TCP failures (busy, timeout under load) should not poison the
		// entire route for all subsequent requests. Only X-Route-Dead from
		// the gateway (explicit tunnel death) should set RouteDead.
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return nil
	}

	connectReq := fmt.Sprintf(
		"CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n",
		targetHost, targetHost, gwAuth,
	)
	if _, err := gatewayConn.Write([]byte(connectReq)); err != nil {
		gatewayConn.Close()
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return nil
	}

	// Parse gateway response. Use bufio.Reader so we don't lose any tunnel
	// data that arrives right after the HTTP response headers.
	gwBuf := bufio.NewReader(gatewayConn)
	resp, err := http.ReadResponse(gwBuf, nil)
	if err != nil {
		gatewayConn.Close()
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return nil
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		// Success — track exit IP
		if ip := resp.Header.Get("X-Exit-IP"); ip != "" && s.OnExitIPChanged != nil {
			s.OnExitIPChanged(ip)
		}
		// If the bufio.Reader buffered tunnel data beyond the HTTP headers,
		// wrap the connection so relay reads from the buffer first.
		if gwBuf.Buffered() > 0 {
			return &bufferedConn{Conn: gatewayConn, r: gwBuf}
		}
		return gatewayConn

	case resp.StatusCode == 402:
		// Balance depleted
		s.routeState.Store(RouteDepleted)
		clientConn.Write([]byte("HTTP/1.1 402 Payment Required\r\n\r\nbalance_depleted"))
		gatewayConn.Close()
		s.upstreamDeadWithReason("depleted")
		return nil

	case resp.StatusCode == 407:
		// Auth failure — session token invalid. Do NOT mark route dead.
		clientConn.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\n\r\n"))
		gatewayConn.Close()
		s.upstreamDeadWithReason("auth_invalid")
		return nil

	case resp.Header.Get("X-Route-Dead") == "true":
		// Gateway says route/tunnel is dead
		s.routeState.Store(RouteDead)
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		gatewayConn.Close()
		s.upstreamDeadWithReason("connection_failed")
		return nil

	case resp.StatusCode >= 500:
		// Transient gateway error — do NOT mark dead
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		gatewayConn.Close()
		return nil

	default:
		// Other non-200 — forward to client, don't mark dead
		clientConn.Write([]byte(fmt.Sprintf("HTTP/1.1 %s\r\n\r\n", resp.Status)))
		gatewayConn.Close()
		return nil
	}
}

// handleConnect handles HTTPS tunneling via HTTP CONNECT.
func (s *Server) handleConnect(ctx context.Context, clientConn net.Conn, initialData string) {
	if s.checkRouteState(clientConn) {
		return
	}
	if s.checkBudget(clientConn) {
		return
	}

	firstLine := initialData
	if idx := strings.Index(initialData, "\r\n"); idx != -1 {
		firstLine = initialData[:idx]
	}
	parts := strings.SplitN(firstLine, " ", 3)
	targetHost := parts[1]

	gatewayConn := s.dialAndSendConnect(clientConn, targetHost)
	if gatewayConn == nil {
		return
	}
	defer gatewayConn.Close()

	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Track relay connection for run-mode force-close
	s.trackConn(gatewayConn)
	s.relay(ctx, clientConn, gatewayConn)
	s.untrackConn(gatewayConn)

	if s.OnRelayDone != nil {
		s.OnRelayDone()
	}
}

// handleHTTP handles plain HTTP requests by forwarding to gateway.
func (s *Server) handleHTTP(ctx context.Context, clientConn net.Conn, initialData []byte) {
	if s.checkRouteState(clientConn) {
		return
	}
	if s.checkBudget(clientConn) {
		return
	}

	s.mu.Lock()
	gwAuth := s.gatewayAuth
	s.mu.Unlock()

	gatewayConn, err := s.dialGateway()
	if err != nil {
		// Don't mark route dead on transient dial failure — matches handleConnect behavior.
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer gatewayConn.Close()

	req := string(initialData)
	authHeader := fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", gwAuth)

	if idx := strings.Index(req, "\r\n"); idx != -1 {
		req = req[:idx+2] + authHeader + req[idx+2:]
	}

	s.meter.AddUp(int64(len(req)))
	if _, err := gatewayConn.Write([]byte(req)); err != nil {
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}

	s.trackConn(gatewayConn)
	s.relay(ctx, clientConn, gatewayConn)
	s.untrackConn(gatewayConn)
}

// relay copies data bidirectionally between two connections, metering bytes.
func (s *Server) relay(ctx context.Context, client, remote net.Conn) {
	done := make(chan struct{}, 2)

	// Idle timeout: if no data flows in either direction for 60 seconds,
	// close both sides. Without this, a dead WireGuard tunnel leaves the
	// relay stuck indefinitely (especially over TLS where half-close doesn't
	// propagate cleanly).
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

func (s *Server) dialGateway() (net.Conn, error) {
	s.mu.Lock()
	addr := s.gatewayAddr
	tls := s.gatewayTLS
	s.mu.Unlock()
	return s.dialGatewayWith(addr, tls)
}

func (s *Server) dialGatewayWith(addr string, useTLS bool) (net.Conn, error) {
	if useTLS {
		return tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{})
	}
	return net.DialTimeout("tcp", addr, 10*time.Second)
}

func (s *Server) upstreamDeadWithReason(reason string) {
	if s.OnUpstreamDead != nil {
		s.OnUpstreamDead(reason)
	}
}

// meteredWriter wraps a writer and counts bytes.
type meteredWriter struct {
	w     io.Writer
	meter *meter.Meter
	isUp  bool
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

package proxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/shellroute/shellroute-cli/internal/meter"
)

// Route state constants for the local proxy health gate.
const (
	RouteHealthy  int32 = 0
	RouteDead     int32 = 1
	RouteRotating int32 = 2
	RouteDepleted int32 = 3
)

// Server is a local HTTP proxy that forwards traffic to an upstream: the
// hosted gateway or a user's own proxy.
type Server struct {
	listenAddr      string
	mu              sync.Mutex
	upstream        Upstream
	meter           *meter.Meter
	listener        net.Listener
	activeConns     int
	OnUpstreamDead  func(reason string) // called on upstream failure
	OnExitIPChanged func(ip string)     // called when the gateway reports a different exit IP
	OnRelayDone     func()              // called after each relay ends
	budget          atomic.Int64        // byte budget from API (-1 = unlimited)

	// Route health gate — checked before every CONNECT/HTTP
	routeState atomic.Int32 // RouteHealthy, RouteDead, RouteRotating, RouteDepleted

	// A user's own proxy never reports route death, so consecutive failures
	// are counted instead. 0 disables the counter (hosted gateway).
	failureThreshold int
	failures         atomic.Int32

	// Tracked relay connections for run-mode force-close
	trackedMu    sync.Mutex
	trackedConns map[net.Conn]struct{}
	trackRelays  bool // true in run mode — track relay conns for force-close
}

// Config holds proxy server configuration.
type Config struct {
	ListenPort  int
	Upstream    Upstream
	Meter       *meter.Meter
	ByteBudget  int64 // max bytes allowed (-1 = unlimited, 0 = blocked)
	TrackRelays bool  // true = track active relay conns for force-close (run mode)
	// FailureThreshold marks the route dead after this many consecutive
	// upstream failures. 0 disables the counter; the hosted gateway reports
	// death explicitly instead.
	FailureThreshold int
}

func NewServer(cfg *Config) *Server {
	budget := cfg.ByteBudget
	if budget == 0 {
		budget = -1
	}
	s := &Server{
		listenAddr:       fmt.Sprintf("127.0.0.1:%d", cfg.ListenPort),
		meter:            cfg.Meter,
		trackRelays:      cfg.TrackRelays,
		trackedConns:     make(map[net.Conn]struct{}),
		failureThreshold: cfg.FailureThreshold,
	}
	s.budget.Store(budget)
	s.SetUpstream(cfg.Upstream)
	return s
}

// exitIPNotifier is implemented by upstreams that learn the exit IP from the
// gateway on each tunnel.
type exitIPNotifier interface {
	setExitIPCallback(func(string))
}

// SetUpstream replaces the upstream with no listener gap and marks the route
// healthy. Same port, new destination.
func (s *Server) SetUpstream(u Upstream) {
	if n, ok := u.(exitIPNotifier); ok {
		n.setExitIPCallback(func(ip string) {
			if s.OnExitIPChanged != nil {
				s.OnExitIPChanged(ip)
			}
		})
	}
	s.mu.Lock()
	s.upstream = u
	s.mu.Unlock()
	s.failures.Store(0)
	s.routeState.Store(RouteHealthy)
}

// SwapUpstream replaces the hosted gateway credentials for rotation.
func (s *Server) SwapUpstream(gatewayAddr string, gatewayTLS bool, token string) {
	s.SetUpstream(NewHostedUpstream(gatewayAddr, gatewayTLS, token))
}

// Upstream returns the current upstream.
func (s *Server) Upstream() Upstream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upstream
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

// dialAndSendConnect opens a tunnel to targetHost through the current
// upstream. On failure it answers the client and returns nil.
func (s *Server) dialAndSendConnect(ctx context.Context, clientConn net.Conn, targetHost string) net.Conn {
	up := s.Upstream()
	if up == nil {
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return nil
	}
	conn, err := up.DialTarget(ctx, targetHost)
	if err != nil {
		s.handleDialError(clientConn, err)
		return nil
	}
	s.failures.Store(0)
	return conn
}

// handleDialError answers the client and updates route state after an
// upstream failure. Hosted replies keep their meaning (402 depleted, 407
// token invalid, X-Route-Dead); a user's own proxy is marked dead on a
// credentials error or after failureThreshold consecutive failures.
func (s *Server) handleDialError(clientConn net.Conn, err error) {
	var de *DialError
	if !errors.As(err, &de) {
		de = &DialError{Kind: KindDial, Err: err}
	}
	switch de.Kind {
	case KindDepleted:
		s.routeState.Store(RouteDepleted)
		clientConn.Write([]byte("HTTP/1.1 402 Payment Required\r\n\r\nbalance_depleted"))
		s.upstreamDeadWithReason("depleted")
	case KindCredentials:
		clientConn.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\n\r\n"))
		if s.failureThreshold > 0 {
			// Retrying cannot fix a wrong password.
			s.SetRouteDead(s.trackRelays)
		}
		s.upstreamDeadWithReason("auth_invalid")
	case KindRouteDead:
		s.routeState.Store(RouteDead)
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		s.upstreamDeadWithReason("connection_failed")
	case KindProxyStatus:
		switch {
		case de.Status >= 500:
			// Transient upstream error for one request.
			clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			s.noteFailure()
		case de.Status >= 100:
			// The proxy refused this target; forward its answer.
			clientConn.Write([]byte(fmt.Sprintf("HTTP/1.1 %s\r\n\r\n", de.Msg)))
		case de.Status == 2:
			// SOCKS: connection not allowed by ruleset.
			clientConn.Write([]byte("HTTP/1.1 403 Forbidden\r\n\r\n"))
		default:
			// SOCKS: target unreachable or refused.
			clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		}
	default:
		// The proxy could not be reached or did not speak the protocol.
		// For the hosted gateway this is transient and never poisons the
		// route; only an explicit X-Route-Dead does.
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		s.noteFailure()
	}
}

// noteFailure counts a consecutive upstream failure for a user's own proxy
// and marks the route dead at the threshold.
func (s *Server) noteFailure() {
	if s.failureThreshold <= 0 {
		return
	}
	if int(s.failures.Add(1)) >= s.failureThreshold {
		s.SetRouteDead(s.trackRelays)
		s.upstreamDeadWithReason("connection_failed")
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

	upstreamConn := s.dialAndSendConnect(ctx, clientConn, targetHost)
	if upstreamConn == nil {
		return
	}
	defer upstreamConn.Close()

	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Track relay connection for run-mode force-close
	s.trackConn(upstreamConn)
	s.relay(ctx, clientConn, upstreamConn)
	s.untrackConn(upstreamConn)

	if s.OnRelayDone != nil {
		s.OnRelayDone()
	}
}

// handleHTTP handles plain HTTP requests. An HTTP proxy upstream takes the
// request as-is in absolute form with its own Proxy-Authorization; any other
// upstream gets a tunnel to the target and the request in origin form.
// No explicit body size limit: the relay meters all bytes (billing is correct)
// and the gateway enforces the byte budget. The idle timeout (60s) bounds
// slow-drip attacks.
func (s *Server) handleHTTP(ctx context.Context, clientConn net.Conn, initialData []byte) {
	if s.checkRouteState(clientConn) {
		return
	}
	if s.checkBudget(clientConn) {
		return
	}

	up := s.Upstream()
	if up == nil {
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}

	var upstreamConn net.Conn
	var req []byte
	if hp, ok := up.(HTTPProxyUpstream); ok {
		conn, err := hp.DialProxy(ctx)
		if err != nil {
			s.handleDialError(clientConn, err)
			return
		}
		upstreamConn = conn
		req = initialData
		if auth := hp.ProxyAuthorization(); auth != "" {
			text := string(initialData)
			if idx := strings.Index(text, "\r\n"); idx != -1 {
				text = text[:idx+2] + "Proxy-Authorization: " + auth + "\r\n" + text[idx+2:]
			}
			req = []byte(text)
		}
	} else {
		target, origin, err := originForm(initialData)
		if err != nil {
			clientConn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			return
		}
		conn, err := up.DialTarget(ctx, target)
		if err != nil {
			s.handleDialError(clientConn, err)
			return
		}
		upstreamConn = conn
		req = origin
	}
	defer upstreamConn.Close()
	s.failures.Store(0)

	s.meter.AddUp(int64(len(req)))
	if _, err := upstreamConn.Write(req); err != nil {
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}

	s.trackConn(upstreamConn)
	s.relay(ctx, clientConn, upstreamConn)
	s.untrackConn(upstreamConn)
}

func (s *Server) upstreamDeadWithReason(reason string) {
	if s.OnUpstreamDead != nil {
		s.OnUpstreamDead(reason)
	}
}

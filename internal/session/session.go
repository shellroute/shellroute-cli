package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/meter"
	"github.com/shellroute/shellroute-cli/internal/proxy"
)

// Session represents an active proxy session.
type Session struct {
	ID         string
	Country    string
	City       string
	Type       string
	Mode       string // "proxy", "run", or "" (interactive)
	Gateway    api.GatewayInfo
	Port       int
	StartTime  time.Time
	BalanceUSD float64
	LowBalance bool

	exitIPMu sync.Mutex
	exitIP   string

	client *api.Client
	meter  *meter.Meter
	proxy  *proxy.Server
	cancel context.CancelFunc

	OnUpstreamDead func(reason string) // "depleted" or "connection_failed"
	OnExitIPUpdate func(ip string)     // called by proxy when X-Exit-IP changes
	OnRelayDone    func()              // called after each relay ends
}

// GetExitIP returns the current exit IP (thread-safe).
func (s *Session) GetExitIP() string {
	s.exitIPMu.Lock()
	defer s.exitIPMu.Unlock()
	return s.exitIP
}

// SetExitIP updates the exit IP (thread-safe).
func (s *Session) SetExitIP(ip string) {
	s.exitIPMu.Lock()
	defer s.exitIPMu.Unlock()
	s.exitIP = ip
}

// Info is the serializable session state written to the PID file.
type Info struct {
	PID       int    `json:"pid"`
	SessionID string `json:"session_id"`
	Country   string `json:"country"`
	City      string `json:"city"`
	Type      string `json:"type"`
	ExitIP    string `json:"exit_ip"`
	Port      int    `json:"port"`
	StartTime string `json:"start_time"`
	Mode      string `json:"mode,omitempty"` // "proxy", "run", or "" (interactive)
}

// StartOpts holds optional parameters for Start.
type StartOpts struct {
	TrackRelays    bool          // true for run mode — enables force-close of active relay conns
	ConnectTimeout time.Duration // override exit IP detection timeout (default 45s)
	Mode           string        // "proxy", "run", or "" (interactive)
}

// StartWithCredentials starts a local proxy using pre-existing session credentials
// (from a rotate response). Does not call the API to create a session.
func StartWithCredentials(ctx context.Context, client *api.Client, resp *api.SessionCreateResponse, port int) (*Session, error) {
	return startWithResponse(ctx, client, resp, port, StartOpts{})
}

// Start creates a new session via the API and starts the local proxy.
func Start(ctx context.Context, client *api.Client, req *api.SessionCreateRequest, port int, opts ...StartOpts) (*Session, error) {
	// Bind port before API call — fail fast if port unavailable
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil && port != 0 {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return nil, fmt.Errorf("no available port: %w", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	resp, err := client.CreateSession(req)
	if err != nil {
		return nil, err
	}

	var sOpts StartOpts
	if len(opts) > 0 {
		sOpts = opts[0]
	}
	return startWithResponse(ctx, client, resp, port, sOpts)
}

// startWithResponse is the shared implementation for Start and StartWithCredentials.
func startWithResponse(ctx context.Context, client *api.Client, resp *api.SessionCreateResponse, port int, opts StartOpts) (*Session, error) {
	// If port not yet bound (StartWithCredentials path), bind now
	if port == 0 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("no available port: %w", err)
		}
		port = ln.Addr().(*net.TCPAddr).Port
		ln.Close()
	} else {
		// Verify port is available (Start already closed its listener)
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			ln, err = net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, fmt.Errorf("no available port: %w", err)
			}
			port = ln.Addr().(*net.TCPAddr).Port
		}
		ln.Close()
	}

	m := meter.New()
	proxyServer := proxy.NewServer(&proxy.Config{
		ListenPort:  port,
		GatewayAddr: resp.Gateway.Endpoint,
		GatewayTLS:  gatewayNeedsTLS(resp.Gateway.Endpoint),
		Token:       resp.Gateway.Token,
		Meter:       m,
		ByteBudget:  resp.ByteBudget,
		TrackRelays: opts.TrackRelays,
	})

	ctx, cancel := context.WithCancel(ctx)

	s := &Session{
		ID:         resp.SessionID,
		Country:    resp.Country,
		City:       resp.City,
		Type:       resp.Type,
		Mode:       opts.Mode,
		exitIP:     resp.ExitIP,
		Gateway:    resp.Gateway,
		Port:       port,
		StartTime:  time.Now(),
		BalanceUSD: resp.BalanceUSD,
		LowBalance: resp.LowBalance,
		client:     client,
		meter:      m,
		proxy:      proxyServer,
		cancel:     cancel,
	}

	// Wire callbacks
	proxyServer.OnUpstreamDead = func(reason string) {
		if s.OnUpstreamDead != nil {
			s.OnUpstreamDead(reason)
		}
	}
	proxyServer.OnExitIPChanged = func(ip string) {
		if s.OnExitIPUpdate != nil {
			s.OnExitIPUpdate(ip)
		} else {
			s.SetExitIP(ip)
		}
	}

	proxyServer.OnRelayDone = func() {
		if s.OnRelayDone != nil {
			s.OnRelayDone()
		}
	}

	if err := s.writeInfoFile(); err != nil {
		cancel()
		return nil, fmt.Errorf("write session file: %w", err)
	}

	go func() {
		if err := proxyServer.Start(ctx); err != nil {
			_ = err
			s.removeInfoFile()
		}
	}()

	// Wait for the tunnel to be ready.
	// If the API already returned an exit IP (gateway had a pool entry), skip
	// the slow detectExitIPRetry — the tunnel is proven healthy.
	connectTimeout := 45 * time.Second
	if opts.ConnectTimeout > 0 {
		connectTimeout = opts.ConnectTimeout
	}
	connectStart := time.Now()
	if s.GetExitIP() == "" {
		if ip := detectExitIPRetry(port, connectTimeout); ip != "" {
			s.SetExitIP(ip)
		}
	}
	connectMs := int(time.Since(connectStart).Milliseconds())

	if s.GetExitIP() != "" {
		client.ReportSessionConnect(s.ID, s.Country, s.Type, true, connectMs, "")
	} else {
		client.ReportSessionConnect(s.ID, s.Country, s.Type, false, connectMs, "no exit IP after timeout")
	}

	return s, nil
}

// Stop gracefully shuts down the session.
// Does NOT report usage — gateway is the authoritative meter.
func (s *Session) Stop() (*api.SessionEndResponse, error) {
	s.cancel()

	// End session with backend (triggers outbox cleanup + min charge)
	resp, err := s.client.EndSession(s.ID)

	// Clean up info file
	s.removeInfoFile()

	if err != nil {
		return nil, err
	}
	return resp, nil
}

// Meter returns the session's bandwidth meter.
func (s *Session) Meter() *meter.Meter {
	return s.meter
}

// Duration returns how long the session has been active.
func (s *Session) Duration() time.Duration {
	return time.Since(s.StartTime)
}

// ProxyURL returns the local proxy URL.
func (s *Session) ProxyURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.Port)
}

// Proxy returns the underlying proxy server (for route state control).
func (s *Session) Proxy() *proxy.Server {
	return s.proxy
}

// SetProxy sets the proxy server (for testing).
func (s *Session) SetProxy(p *proxy.Server) {
	s.proxy = p
}

func (s *Session) sessionFileName() string {
	return fmt.Sprintf("session-%d.json", os.Getpid())
}

func (s *Session) writeInfoFile() error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}

	info := Info{
		PID:       os.Getpid(),
		SessionID: s.ID,
		Country:   s.Country,
		City:      s.City,
		Type:      s.Type,
		ExitIP:    s.GetExitIP(),
		Port:      s.Port,
		StartTime: s.StartTime.Format(time.RFC3339),
		Mode:      s.Mode,
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, s.sessionFileName()), data, 0600)
}

func (s *Session) removeInfoFile() {
	dir, err := config.Dir()
	if err != nil {
		return
	}
	os.Remove(filepath.Join(dir, s.sessionFileName()))
}

// LoadActiveSessions reads all session files and returns those with live processes.
// Cleans up stale files from dead processes.
func LoadActiveSessions() ([]Info, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}

	entries, err := filepath.Glob(filepath.Join(dir, "session-*.json"))
	if err != nil {
		return nil, err
	}

	// Also check legacy single file
	legacy := filepath.Join(dir, "session.json")
	if _, err := os.Stat(legacy); err == nil {
		entries = append(entries, legacy)
	}

	var sessions []Info
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var info Info
		if err := json.Unmarshal(data, &info); err != nil {
			os.Remove(path)
			continue
		}

		// Check if process is still alive
		proc, err := os.FindProcess(info.PID)
		if err != nil {
			os.Remove(path)
			continue
		}
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			os.Remove(path)
			continue
		}

		sessions = append(sessions, info)
	}

	return sessions, nil
}

// gatewayNeedsTLS returns true if the gateway endpoint is not localhost.
func gatewayNeedsTLS(endpoint string) bool {
	host := endpoint
	if idx := strings.LastIndex(endpoint, ":"); idx != -1 {
		host = endpoint[:idx]
	}
	return host != "localhost" && host != "127.0.0.1" && host != "::1"
}

// detectExitIPRetry tries to detect exit IP with retries up to the given timeout.
func detectExitIPRetry(port int, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ip := detectExitIP(port); ip != "" {
			return ip
		}
		time.Sleep(2 * time.Second)
	}
	return ""
}

var ipDetectEndpoints = []string{
	"https://ipinfo.io/ip",
	"https://api.ipify.org",
}

func detectExitIP(port int) string {
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	transport := &http.Transport{
		Proxy: func(*http.Request) (*neturl.URL, error) {
			return neturl.Parse(proxyURL)
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}

	for _, endpoint := range ipDetectEndpoints {
		resp, err := client.Get(endpoint)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		ip := strings.TrimSpace(string(body))
		if ip != "" {
			return ip
		}
	}
	return ""
}

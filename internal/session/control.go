package session

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/shellroute/shellroute-cli/internal/proxy"
)

// Controller manages the session lifecycle, health monitoring, and an
// HTTP control server that shell functions talk to via curl.
type Controller struct {
	mu      sync.Mutex
	sess    *Session
	client  *api.Client
	cfg     *config.Config
	healthy bool

	ctrlPort      int
	probeCount    int
	lastProbeTime time.Time
	server        *http.Server
	cancel        context.CancelFunc
	stopped       bool

	// Recovery probing: require consecutive successful probes before restoring
	successCount int    // consecutive successful probes
	notifiedDead bool   // already sent "connection lost" notification
	deadReason   string // "depleted", "auth_invalid", "connection_failed"
	autoRotating bool   // auto-rotate in progress (from guard path)

	validCountries   map[string]string // code → name
	lowBalanceWarned bool              // already shown low balance warning this session
	lastCheckedBytes int64             // bytes at last SUCCESSFUL route-check API verification
	needsRouteVerify bool              // set by OnRelayDone, cleared by route-check
	connectTimeout   time.Duration     // override for tests (default 45s)

	// Accumulated stats across rotations within this interactive session
	accumDuration int
	accumBytes    int64
	accumCost     float64
}

// NewController creates a controller (no active session yet).
func NewController(client *api.Client, cfg *config.Config) *Controller {
	return &Controller{
		client: client,
		cfg:    cfg,
	}
}

// ConnectDirect sets an already-started session (used by `shellroute connect`).
func (c *Controller) ConnectDirect(sess *Session) {
	c.wireUpstreamCallback(sess)
	c.mu.Lock()
	c.sess = sess
	c.healthy = true
	c.mu.Unlock()
}

// wireUpstreamCallback fires immediately on upstream failure — no debouncing.
// The proxy only calls this on real connection/auth failures, not transient issues.
// Does NOT auto-rotate — rotation only happens at command boundaries (guard path).
func (c *Controller) wireUpstreamCallback(sess *Session) {
	// Thread-safe exit IP tracking — proxy callback → controller lock → session field
	sess.OnExitIPUpdate = func(ip string) {
		c.mu.Lock()
		if c.sess != nil && c.sess.GetExitIP() != ip {
			c.sess.SetExitIP(ip)
		}
		c.mu.Unlock()
	}
	// After relay ends, set a cheap flag — no API calls from relay teardown.
	// /route-check picks up the flag and verifies via API at the command boundary.
	sess.OnRelayDone = func() {
		c.mu.Lock()
		c.needsRouteVerify = true
		c.mu.Unlock()
	}
	sess.OnUpstreamDead = func(reason string) {
		c.mu.Lock()
		wasHealthy := c.healthy
		c.healthy = false
		c.notifiedDead = true
		c.deadReason = reason
		c.mu.Unlock()

		if !wasHealthy {
			return
		}

		switch reason {
		case "depleted":
			sess.Proxy().SetRouteDepleted()
		default:
			sess.Proxy().SetRouteDead(false)
		}
		// No async notifications — precmd handles all UX via healthcheck.
		// Precmd detects "dead" via healthcheck and shows the right prompt.
	}
}

// autoRotateWithContext is like autoRotate but uses the given context for cancellation
// and swaps the upstream credentials instead of killing/restarting the proxy.
func (c *Controller) autoRotateWithContext(ctx context.Context) {
	c.mu.Lock()
	if c.sess == nil {
		c.mu.Unlock()
		return
	}
	oldSessionID := c.sess.ID
	c.mu.Unlock()

	// Check context before API call
	if ctx.Err() != nil {
		return
	}

	opts := &api.RotateOpts{}
	rotateResp, err := c.client.RotateSession(oldSessionID, opts)
	if err != nil {
		// Rotation failed — ensure proxy goes back to dead (not stuck on rotating)
		c.mu.Lock()
		if c.sess != nil && c.sess.Proxy() != nil {
			c.sess.Proxy().SetRouteDead(false)
		}
		c.mu.Unlock()
		return
	}

	// Swap upstream on the existing proxy — no listener gap
	c.mu.Lock()
	if c.sess != nil && c.sess.Proxy() != nil {
		c.sess.removeInfoFile() // clean up old session file
		c.sess.Proxy().SwapUpstream(
			rotateResp.Gateway.Endpoint,
			gatewayNeedsTLS(rotateResp.Gateway.Endpoint),
			rotateResp.Gateway.Token,
		)
		// Update session metadata
		c.sess.ID = rotateResp.SessionID
		c.sess.SetExitIP(rotateResp.ExitIP)
		c.sess.Gateway = rotateResp.Gateway
		c.sess.writeInfoFile() // write new session file
	}
	c.mu.Unlock()

	// Detect exit IP if not returned — also guards StopAndDisconnect race
	c.mu.Lock()
	sess := c.sess
	c.mu.Unlock()
	if sess == nil {
		return
	}
	if sess.GetExitIP() == "" {
		if ip := detectExitIPRetry(sess.Port, 15*time.Second); ip != "" {
			sess.SetExitIP(ip)
		}
	}

	if sess.GetExitIP() == "" {
		// Rotation failed to get an exit IP — mark dead
		sess.Proxy().SetRouteDead(false)
		return
	}

	c.wireUpstreamCallback(sess)
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.healthy = true
	c.notifiedDead = false
	c.deadReason = ""
	c.successCount = 0
	c.mu.Unlock()
}

// CtrlPort returns the control server port.
func (c *Controller) CtrlPort() int {
	return c.ctrlPort
}

// Start launches the HTTP control server and health monitor.
func (c *Controller) Start() (int, error) {
	// Load valid countries from API
	c.validCountries = make(map[string]string)
	if locs, err := c.client.GetLocations(); err == nil {
		for _, loc := range locs.Locations {
			c.validCountries[loc.Country] = loc.CountryName
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("control server: %w", err)
	}
	c.ctrlPort = ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/connect", c.httpConnect)
	mux.HandleFunc("/disconnect", c.httpDisconnect)
	mux.HandleFunc("/rotate", c.httpRotate)
	mux.HandleFunc("/status", c.httpStatus)
	mux.HandleFunc("/healthcheck", c.httpHealthCheck)
	mux.HandleFunc("/route-check", c.httpRouteCheck)
	mux.HandleFunc("/rotate-and-wait", c.httpRotateAndWait)
	mux.HandleFunc("/countries", c.httpCountries)
	mux.HandleFunc("/balance", c.httpBalance)

	c.server = &http.Server{Handler: mux}

	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancel = cancel
	c.stopped = false
	c.mu.Unlock()

	go c.server.Serve(ln)
	go c.monitor(ctx)
	go c.balanceMonitor(ctx)

	return c.ctrlPort, nil
}

// Stop shuts down the controller and disconnects if active.
func (c *Controller) Stop() {
	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
	}
	if c.server != nil {
		c.server.Close()
	}
}

// StopAndDisconnect stops the controller and returns the final session
// summary with accumulated stats from rotations. Returns nil if no session was active.
func (c *Controller) StopAndDisconnect() *api.SessionEndResponse {
	c.Stop()

	c.mu.Lock()
	sess := c.sess
	c.sess = nil
	accumDur := c.accumDuration
	accumBytes := c.accumBytes
	accumCost := c.accumCost
	c.accumDuration = 0
	c.accumBytes = 0
	c.accumCost = 0
	c.mu.Unlock()

	if sess == nil {
		return nil
	}

	resp, _ := sess.Stop()
	if resp != nil {
		resp.DurationSec += accumDur
		resp.BytesTotal += accumBytes
		resp.CostUSD += accumCost
	}
	return resp
}

// Session returns the current session (nil if disconnected).
func (c *Controller) Session() *Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess
}

// --- HTTP handlers ---

// /connect?country=XX — returns env exports for eval
func (c *Controller) httpConnect(w http.ResponseWriter, r *http.Request) {
	country := config.NormalizeCountry(r.URL.Query().Get("country"))
	if country == "" {
		w.WriteHeader(400)
		fmt.Fprintln(w, "ERROR Country required. Use /connect <country>.")
		return
	}

	if len(c.validCountries) > 0 {
		if _, ok := c.validCountries[country]; !ok {
			w.WriteHeader(400)
			fmt.Fprintf(w, "ERROR Unknown country code: %s. Run /countries to see available.\n", country)
			return
		}
	}

	c.mu.Lock()
	oldSess := c.sess
	c.mu.Unlock()

	ipType := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("iptype")))
	if ipType == "" {
		ipType = c.cfg.DefaultType
	}
	city := strings.TrimSpace(r.URL.Query().Get("city"))
	sticky := r.URL.Query().Get("sticky") == "1"

	// Start new session on a temp port (old proxy still running).
	// Only disconnect old session after new one is confirmed working.
	sess, err := Start(
		context.Background(),
		c.client,
		&api.SessionCreateRequest{
			Country: country,
			City:    city,
			Type:    ipType,
			Sticky:  sticky,
		},
		0, // temp port — old proxy still on the configured port
		StartOpts{ConnectTimeout: c.connectTimeout},
	)
	if err != nil {
		// Old session stays alive
		if apiErr, ok := err.(*api.APIError); ok {
			fmt.Fprintf(w, "ERROR %s\n", apiErr.UserMessage())
		} else {
			fmt.Fprintln(w, "ERROR Connection failed. Try again or contact support.")
		}
		return
	}

	// If exit IP wasn't detected during Start(), retry with longer
	// timeout — the first request triggers the gateway to create a tunnel
	// connection which takes 8-10s.
	if sess.GetExitIP() == "" {
		retryTimeout := 15 * time.Second
		if c.connectTimeout > 0 {
			retryTimeout = c.connectTimeout
		}
		if ip := detectExitIPRetry(sess.Port, retryTimeout); ip != "" {
			sess.SetExitIP(ip)
		}
	}

	// If still no exit IP, the proxy isn't working — clean up, keep old session
	if sess.GetExitIP() == "" {
		sess.Stop()
		fmt.Fprintln(w, "ERROR Connection failed. Try again or contact support.")
		return
	}

	// New session works — now disconnect old session
	if oldSess != nil {
		c.mu.Lock()
		c.sess = nil
		c.mu.Unlock()
		oldSess.Stop()
	}

	// Wire kill switch AFTER connection is established — probe failures
	// during setup are expected and should not trigger the kill switch.
	c.wireUpstreamCallback(sess)

	c.mu.Lock()
	c.sess = sess
	c.healthy = true
	c.notifiedDead = false
	c.successCount = 0
	c.lowBalanceWarned = false
	c.accumDuration = 0
	c.accumBytes = 0
	c.accumCost = 0
	c.mu.Unlock()

	proxyURL := sess.ProxyURL()
	fmt.Fprintf(w, "export HTTP_PROXY=%s\n", proxyURL)
	fmt.Fprintf(w, "export HTTPS_PROXY=%s\n", proxyURL)
	fmt.Fprintf(w, "export http_proxy=%s\n", proxyURL)
	fmt.Fprintf(w, "export https_proxy=%s\n", proxyURL)
	fmt.Fprintf(w, "export NO_PROXY=localhost,127.0.0.1\n")
	fmt.Fprintf(w, "export no_proxy=localhost,127.0.0.1\n")
	fmt.Fprintf(w, "export SHELLROUTE_SESSION_ID=%s\n", sess.ID)
	fmt.Fprintf(w, "export SHELLROUTE_COUNTRY=%s\n", sess.Country)
	fmt.Fprintf(w, "export SHELLROUTE_COUNTRY_NAME=%s\n", shellQuote(c.countryName(sess.Country)))
	fmt.Fprintf(w, "export SHELLROUTE_CITY=%s\n", shellQuote(sess.City))
	fmt.Fprintf(w, "export SHELLROUTE_EXIT_IP=%s\n", sess.GetExitIP())
	fmt.Fprintf(w, "export SHELLROUTE_PORT=%d\n", sess.Port)
	if sess.LowBalance {
		fmt.Fprintf(w, "LOW_BALANCE=%.2f\n", math.Floor(sess.BalanceUSD*100)/100)
	}
}

// /disconnect — returns formatted summary
func (c *Controller) httpDisconnect(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	sess := c.sess
	c.mu.Unlock()

	if sess == nil {
		w.WriteHeader(400)
		fmt.Fprintln(w, "Not connected.")
		return
	}

	resp, err := sess.Stop()
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprintln(w, "ERROR Failed to disconnect. Try again.")
		return
	}

	// Add final session stats to accumulated totals
	c.mu.Lock()
	c.sess = nil
	totalDuration := c.accumDuration + resp.DurationSec
	totalBytes := c.accumBytes + resp.BytesTotal
	totalCost := c.accumCost + resp.CostUSD
	c.accumDuration = 0
	c.accumBytes = 0
	c.accumCost = 0
	c.mu.Unlock()

	display.WriteSessionSummary(w, "Disconnected.", totalDuration, totalBytes, totalCost, resp.BalanceUSD)
}

// /rotate — calls API rotate endpoint, which handles gateway cleanup
func (c *Controller) httpRotate(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	if c.sess == nil {
		c.mu.Unlock()
		w.WriteHeader(400)
		fmt.Fprintln(w, "Not connected.")
		return
	}
	if !c.healthy {
		c.mu.Unlock()
		fmt.Fprintln(w, "DISCONNECTED=1")
		fmt.Fprintln(w, "ERROR Connection lost. Run /connect to reconnect.")
		return
	}

	oldSessionID := c.sess.ID
	c.mu.Unlock()

	// Pass optional overrides from query params (set by shell from SHELLROUTE_IPTYPE/STICKY)
	opts := &api.RotateOpts{
		IPType: r.URL.Query().Get("iptype"),
		Sticky: r.URL.Query().Get("sticky"),
	}

	// Call API rotate — if it fails, keep old session alive
	rotateResp, err := c.client.RotateSession(oldSessionID, opts)
	if err != nil {
		if apiErr, ok := err.(*api.APIError); ok {
			switch apiErr.Code {
			case "no_alternatives":
				fmt.Fprintf(w, "NO_OTHER_IPS=%s\n", apiErr.UserMessage())
				return
			case "not_found":
				fmt.Fprintln(w, "DISCONNECTED=1")
				fmt.Fprintln(w, "ERROR Session expired. Run /connect to reconnect.")
				return
			}
		}
		w.WriteHeader(500)
		fmt.Fprintln(w, "ERROR Failed to rotate. Try again.")
		return
	}

	// Accumulate old session stats
	if rotateResp.PrevSession != nil {
		c.mu.Lock()
		c.accumDuration += rotateResp.PrevSession.DurationSec
		c.accumBytes += rotateResp.PrevSession.BytesTotal
		c.accumCost += rotateResp.PrevSession.CostUSD
		c.mu.Unlock()
	}

	// Swap upstream credentials on existing proxy — no listener gap
	c.mu.Lock()
	if c.sess != nil && c.sess.Proxy() != nil {
		c.sess.removeInfoFile()
		c.sess.Proxy().SwapUpstream(
			rotateResp.Gateway.Endpoint,
			gatewayNeedsTLS(rotateResp.Gateway.Endpoint),
			rotateResp.Gateway.Token,
		)
		c.sess.ID = rotateResp.SessionID
		c.sess.SetExitIP(rotateResp.ExitIP)
		c.sess.Gateway = rotateResp.Gateway
		c.sess.writeInfoFile()
	}
	c.mu.Unlock()

	// Detect exit IP if not returned
	c.mu.Lock()
	sess := c.sess
	c.mu.Unlock()
	if sess != nil && sess.GetExitIP() == "" {
		if ip := detectExitIPRetry(sess.Port, 20*time.Second); ip != "" {
			sess.SetExitIP(ip)
		}
	}

	if sess == nil || sess.GetExitIP() == "" {
		fmt.Fprintln(w, "DISCONNECTED=1")
		fmt.Fprintln(w, "ERROR Rotation failed. Run /connect to reconnect.")
		return
	}

	c.wireUpstreamCallback(sess)

	c.mu.Lock()
	c.healthy = true
	c.notifiedDead = false
	c.deadReason = ""
	c.successCount = 0
	c.mu.Unlock()

	fmt.Fprintf(w, "export SHELLROUTE_SESSION_ID=%s\n", sess.ID)
	fmt.Fprintf(w, "export SHELLROUTE_EXIT_IP=%s\n", sess.GetExitIP())
	fmt.Fprintf(w, "export SHELLROUTE_COUNTRY=%s\n", sess.Country)
	fmt.Fprintf(w, "export SHELLROUTE_COUNTRY_NAME=%s\n", shellQuote(c.countryName(sess.Country)))
}

// /healthcheck — fast check for shell precmd/guard.
// Returns health state from the proxy route state + controller state.
func (c *Controller) httpHealthCheck(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	sess := c.sess
	healthy := c.healthy
	rotating := c.autoRotating
	deadReason := c.deadReason
	c.mu.Unlock()

	w.Header().Set("Content-Type", "text/plain")
	if sess == nil {
		fmt.Fprint(w, "none")
		return
	}

	if rotating {
		fmt.Fprint(w, "rotating")
		return
	}

	if healthy {
		c.mu.Lock()
		ip := ""
		if c.sess != nil {
			ip = c.sess.GetExitIP()
		}
		c.mu.Unlock()
		fmt.Fprintf(w, "ok %s", ip)
	} else if deadReason == "depleted" {
		fmt.Fprint(w, "dead depleted")
	} else {
		fmt.Fprint(w, "dead")
	}
}

// markDead sets both controller and proxy state to dead/depleted.
// Only mutates if sess is still the current session (#5).
func (c *Controller) markDead(sess *Session, reason string) {
	c.mu.Lock()
	if c.sess == nil || c.sess.ID != sess.ID {
		c.mu.Unlock()
		return
	}
	c.healthy = false
	c.deadReason = reason
	c.notifiedDead = true
	c.mu.Unlock()
	if sess.Proxy() != nil {
		if reason == "depleted" {
			sess.Proxy().SetRouteDepleted()
		} else {
			sess.Proxy().SetRouteDead(false)
		}
	}
}

// /route-check — called by the accept-line guard before each command.
// Checks local proxy state first (short-circuit for depleted/rotating).
// For dead state, could query gateway for recovery in the future.
// Returns: "ok <exit_ip>", "dead", "dead depleted", "rotating", "none"
func (c *Controller) httpRouteCheck(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	sess := c.sess
	healthy := c.healthy
	rotating := c.autoRotating
	deadReason := c.deadReason
	c.mu.Unlock()

	w.Header().Set("Content-Type", "text/plain")
	if sess == nil {
		fmt.Fprint(w, "none")
		return
	}

	// Short-circuit on terminal local states
	if rotating {
		fmt.Fprint(w, "rotating")
		return
	}
	if deadReason == "depleted" && !healthy {
		fmt.Fprint(w, "dead depleted")
		return
	}

	if !healthy {
		fmt.Fprint(w, "dead")
		return
	}

	// Check if the proxy detected depletion/death via route state.
	// Sync controller state via markDead for consistency.
	if sess.Proxy() != nil {
		proxyState := sess.Proxy().RouteState()
		if proxyState == proxy.RouteDepleted {
			c.markDead(sess, "depleted")
			fmt.Fprint(w, "dead depleted")
			return
		}
		if proxyState == proxy.RouteDead {
			c.markDead(sess, "connection_failed")
			fmt.Fprint(w, "dead")
			return
		}
	}

	// Verify session via API when bytes changed or relay just ended.
	// Only clear needsRouteVerify and update lastCheckedBytes after
	// a successful API response — failed calls must retry next time.
	if sess.Proxy() != nil {
		currentBytes := sess.Proxy().BytesTotal()
		c.mu.Lock()
		needsVerify := c.needsRouteVerify || currentBytes != c.lastCheckedBytes
		c.mu.Unlock()

		if needsVerify {
			rh, err := c.client.GetSessionRouteHealth(sess.ID)
			if err != nil {
				// API unreachable — fail closed as dead, do NOT cache.
				// needsRouteVerify stays set → retries next route-check.
				fmt.Fprint(w, "dead")
				return
			}
			// Success — now safe to clear flag and update lastCheckedBytes.
			c.mu.Lock()
			c.needsRouteVerify = false
			c.lastCheckedBytes = currentBytes
			c.mu.Unlock()

			if !rh.Healthy {
				c.markDead(sess, rh.Reason)
				if rh.Reason == "depleted" {
					fmt.Fprint(w, "dead depleted")
				} else {
					fmt.Fprint(w, "dead")
				}
				return
			}
		}
	}

	// Healthy — return ok with exit IP
	c.mu.Lock()
	ip := ""
	if c.sess != nil {
		ip = c.sess.GetExitIP()
	}
	c.mu.Unlock()
	fmt.Fprintf(w, "ok %s", ip)
}

// /rotate-and-wait — synchronous rotation. Called by guard when route is dead.
// Blocks until rotation completes (max 30s). POST only.
// Returns: "ok <new_ip>" or "failed"
func (c *Controller) httpRotateAndWait(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	c.mu.Lock()
	if c.sess == nil {
		c.mu.Unlock()
		fmt.Fprint(w, "failed")
		return
	}
	if c.autoRotating {
		c.mu.Unlock()
		// Already rotating — wait for it (singleflight)
		deadline := time.After(30 * time.Second)
		for {
			select {
			case <-deadline:
				fmt.Fprint(w, "failed")
				return
			case <-r.Context().Done():
				fmt.Fprint(w, "failed")
				return
			case <-time.After(500 * time.Millisecond):
				c.mu.Lock()
				if !c.autoRotating {
					if c.healthy && c.sess != nil {
						ip := c.sess.GetExitIP()
						c.mu.Unlock()
						fmt.Fprintf(w, "ok %s", ip)
						return
					}
					c.mu.Unlock()
					fmt.Fprint(w, "failed")
					return
				}
				c.mu.Unlock()
			}
		}
	}
	c.autoRotating = true
	if c.sess != nil {
		c.sess.Proxy().SetRouteRotating()
	}
	c.mu.Unlock()

	// Run rotation synchronously with request context
	c.autoRotateWithContext(r.Context())

	c.mu.Lock()
	c.autoRotating = false
	if c.healthy && c.sess != nil {
		ip := c.sess.GetExitIP()
		c.mu.Unlock()
		fmt.Fprintf(w, "ok %s", ip)
	} else {
		c.mu.Unlock()
		fmt.Fprint(w, "failed")
	}
}

// /status — plain text session info
func (c *Controller) httpStatus(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sess == nil {
		fmt.Fprintln(w, "Not connected.")
		return
	}

	health := "healthy"
	if !c.healthy {
		health = "unhealthy"
	}
	uptime := time.Since(c.sess.StartTime).Truncate(time.Second)

	fmt.Fprintf(w, "  Country:  %s\n", c.sess.Country)
	fmt.Fprintf(w, "  Exit IP:  %s\n", c.sess.GetExitIP())
	fmt.Fprintf(w, "  Health:   %s\n", health)
	fmt.Fprintf(w, "  Uptime:   %s\n", uptime)
}

// /countries — list available countries
func (c *Controller) httpCountries(w http.ResponseWriter, r *http.Request) {
	locs, err := c.client.GetLocations()
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprintln(w, "ERROR Cannot reach ShellRoute. Check your connection.")
		return
	}

	// If ?country=XX, show cities for that country
	if code := r.URL.Query().Get("country"); code != "" {
		code = config.NormalizeCountry(code)
		for _, loc := range locs.Locations {
			if loc.Country == code {
				if len(loc.Cities) == 0 {
					fmt.Fprintf(w, "  No cities listed for %s\n", code)
					return
				}
				fmt.Fprintf(w, "  Available cities in %s (%s):\n", loc.CountryName, code)
				for _, city := range loc.Cities {
					fmt.Fprintf(w, "    %-20s %d res / %d dc\n", city.Name, city.ResidentialCount, city.DatacenterCount)
				}
				return
			}
		}
		fmt.Fprintf(w, "  Unknown country: %s\n", code)
		return
	}

	fmt.Fprintf(w, "  %-6s %-20s %s\n", "Code", "Country", "IPs")
	fmt.Fprintf(w, "  %-6s %-20s %s\n", "────", "───────", "───")
	for _, loc := range locs.Locations {
		fmt.Fprintf(w, "  %-6s %-20s %d res / %d dc\n",
			loc.Country, loc.CountryName, loc.ResidentialCount, loc.DatacenterCount)
	}
}

// /balance — show current balance
func (c *Controller) httpBalance(w http.ResponseWriter, r *http.Request) {
	bal, err := c.client.GetBalance()
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprintln(w, "ERROR Cannot reach ShellRoute. Check your connection.")
		return
	}
	fmt.Fprintf(w, "  Account: %s\n", bal.Email)
	fmt.Fprintf(w, "  Balance: %s\n", display.FormatBalance(bal.BalanceUSD))
	fmt.Fprintf(w, "  Rate:    $%.2f/GB (residential) | $%.2f/GB (datacenter)\n",
		bal.Rates.ResidentialPerGB, bal.Rates.DatacenterPerGB)
}

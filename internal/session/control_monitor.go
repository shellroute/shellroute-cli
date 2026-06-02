package session

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/proxy"
)

// --- Health monitor ---

const (
	successToRestore = 2 // consecutive successful probes before declaring restored
)

func (c *Controller) monitor(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			if c.sess == nil || c.healthy {
				c.mu.Unlock()
				continue
			}
			port := c.sess.Port
			proxyDead := c.sess.Proxy() != nil && c.sess.Proxy().RouteState() != proxy.RouteHealthy
			c.mu.Unlock()

			// When the proxy route state is dead/rotating/depleted, probeProxy
			// can't work — the local proxy rejects all CONNECT requests with 503.
			// Recovery in this case is handled by the accept-line guard's
			// /rotate-and-wait at the next command boundary. Skip probing.
			if proxyDead {
				continue
			}

			// Only probe when unhealthy — checking if upstream recovered.
			// Dead detection is handled by OnUpstreamDead (immediate, on real requests).
			ok := probeProxy(port)

			c.mu.Lock()
			if c.sess == nil {
				c.mu.Unlock()
				continue
			}
			c.lastProbeTime = time.Now()

			if ok {
				c.successCount++
				if c.successCount >= successToRestore {
					c.healthy = true
					c.successCount = 0
					c.deadReason = ""
					sessPort := c.sess.Port
					c.notifiedDead = false
					// Set proxy route state back to healthy
					if c.sess.Proxy() != nil {
						c.sess.Proxy().SetRouteHealthy()
					}
					c.mu.Unlock()

					// Re-detect exit IP — it may have changed after recovery
					if ip := detectExitIP(sessPort); ip != "" {
						c.mu.Lock()
						if c.sess != nil {
							c.sess.SetExitIP(ip)
						}
						c.mu.Unlock()
					}

					continue
				}
			} else {
				c.successCount = 0
			}
			c.mu.Unlock()
		}
	}
}

// probeProxy tests end-to-end connectivity through the local proxy.
// Sends a CONNECT to a known host via the proxy — if the gateway
// upstream is dead, this fails even though the local port is open.
func probeProxy(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()

	// Send a minimal CONNECT request through the proxy
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	_, err = fmt.Fprintf(conn, "CONNECT httpbin.org:443 HTTP/1.1\r\nHost: httpbin.org:443\r\n\r\n")
	if err != nil {
		return false
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		return false
	}
	return strings.Contains(string(buf[:n]), "200")
}

// balanceMonitor periodically checks the user's balance and shows a one-time
// warning when it drops below $1. Runs every 60s, non-blocking.
func (c *Controller) balanceMonitor(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			sess := c.sess
			isDepleted := c.deadReason == "depleted" && !c.healthy
			c.mu.Unlock()

			if sess == nil {
				continue
			}

			resp, err := c.client.GetBalance(api.BalanceOpts{
				IPType:    sess.Type,
				SessionID: sess.ID,
			})
			if err != nil {
				continue
			}

			// Refresh budget from server-provided byte_budget — catches mid-session top-ups.
			// Does NOT auto-revive depleted sessions — a terminated session
			// is dead server-side (gateway returns 402). User must /connect.
			if !isDepleted && resp.ByteBudget > 0 && sess.Proxy() != nil {
				sess.Proxy().SetBudget(resp.ByteBudget + sess.Proxy().BytesTotal())
			}

			c.mu.Lock()
			if !c.lowBalanceWarned && resp.LowBalance {
				c.lowBalanceWarned = true
			}
			c.mu.Unlock()
		}
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func (c *Controller) countryName(code string) string {
	if name, ok := c.validCountries[code]; ok {
		return name
	}
	return code
}

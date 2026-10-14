package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ExitIPEndpoints are the services asked for the public IP of a route.
var ExitIPEndpoints = []string{
	"https://ipinfo.io/ip",
	"https://api.ipify.org",
}

// ProbeResult is what Probe learned about an upstream.
type ProbeResult struct {
	ExitIP  string
	Latency time.Duration
}

// Probe opens a tunnel through the upstream and asks an IP echo service for
// the exit IP. A *DialError means the tunnel could not be opened; any other
// error means the tunnel opened but no service returned a usable IP.
func Probe(ctx context.Context, up Upstream, timeout time.Duration) (*ProbeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return up.DialTarget(ctx, addr)
		},
		DisableKeepAlives: true,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}

	start := time.Now()
	var lastErr error
	for _, endpoint := range ExitIPEndpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			var de *DialError
			if errors.As(err, &de) {
				return nil, de
			}
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		resp.Body.Close()
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			return &ProbeResult{ExitIP: ip, Latency: time.Since(start)}, nil
		}
		lastErr = fmt.Errorf("unexpected reply from %s", endpoint)
	}
	if lastErr == nil {
		lastErr = errors.New("no exit IP service configured")
	}
	return nil, lastErr
}

// Check verifies that an upstream can open a tunnel: one attempt and one
// retry, each bounded by the dial timeout. A credentials or proxy-status
// failure is final and not retried. A non-*DialError result means the tunnel
// opened but the exit IP could not be read.
func Check(ctx context.Context, up Upstream) (*ProbeResult, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		res, err := Probe(ctx, up, dialTimeout)
		if err == nil {
			return res, nil
		}
		lastErr = err
		var de *DialError
		if !errors.As(err, &de) {
			return nil, err
		}
		if de.Kind == KindCredentials || de.Kind == KindProxyStatus || de.Kind == KindDepleted || ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

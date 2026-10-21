package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ExitIPEndpoints are the plain-text services asked for the public IP of a
// hosted route.
var ExitIPEndpoints = []string{
	"https://ipinfo.io/ip",
	"https://api.ipify.org",
}

// ExitInfoEndpoints answer with JSON naming the caller's public IP ("ip") and,
// when the service knows it, the country ("country"). Used for routes through
// the user's own proxy, where nothing else can tell us where the exit is.
var ExitInfoEndpoints = []string{
	"https://ipinfo.io/json",
	"https://api.ipify.org?format=json",
}

// ProbeResult is what Probe learned about an upstream. Country is what the
// exit IP service reported for the exit IP: a measurement, often right,
// sometimes wrong, and empty when the service does not say.
type ProbeResult struct {
	ExitIP  string
	Country string
	Latency time.Duration
}

// exitInfo is the JSON shape shared by the exit info services.
type exitInfo struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
}

// parseExitInfo accepts a JSON body with "ip" and optional "country", or a
// plain-text IP.
func parseExitInfo(body []byte) (ip, country string) {
	var info exitInfo
	if err := json.Unmarshal(body, &info); err == nil {
		if net.ParseIP(strings.TrimSpace(info.IP)) != nil {
			return strings.TrimSpace(info.IP), strings.ToUpper(strings.TrimSpace(info.Country))
		}
		return "", ""
	}
	text := strings.TrimSpace(string(body))
	if net.ParseIP(text) != nil {
		return text, ""
	}
	return "", ""
}

// ProbeError means the tunnel opened but no exit IP service answered usably.
// Error is a sentence fit for the user; Err keeps the underlying cause for
// errors.Is and errors.As.
type ProbeError struct {
	Msg string
	Err error
}

func (e *ProbeError) Error() string { return e.Msg }
func (e *ProbeError) Unwrap() error { return e.Err }

const (
	probeTimedOut    = "the exit IP service did not answer in time"
	probeUnexpected  = "the exit IP service gave an unexpected reply"
	probeUnreachable = "the exit IP service could not be reached through the proxy"
)

// probeFailure classifies a transport failure after the tunnel opened.
func probeFailure(err error) *ProbeError {
	if netCause(err) == "timeout" {
		return &ProbeError{Msg: probeTimedOut, Err: err}
	}
	return &ProbeError{Msg: probeUnreachable, Err: err}
}

// Probe opens a tunnel through the upstream and asks an exit info service for
// the exit IP and its country. A *DialError means the tunnel could not be
// opened; a *ProbeError means the tunnel opened but no service returned a
// usable IP.
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
	for _, endpoint := range ExitInfoEndpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = &ProbeError{Msg: probeUnreachable, Err: err}
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			var de *DialError
			if errors.As(err, &de) {
				return nil, de
			}
			lastErr = probeFailure(err)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if ip, country := parseExitInfo(body); ip != "" {
			return &ProbeResult{ExitIP: ip, Country: country, Latency: time.Since(start)}, nil
		}
		lastErr = &ProbeError{Msg: probeUnexpected, Err: fmt.Errorf("unexpected reply from %s", endpoint)}
	}
	if lastErr == nil {
		lastErr = &ProbeError{Msg: probeUnreachable, Err: errors.New("no exit IP service configured")}
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

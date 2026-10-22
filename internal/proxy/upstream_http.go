package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"time"
)

// HTTPUpstream is an HTTP proxy reached with CONNECT: the hosted gateway or a
// user's own HTTP or HTTPS proxy.
type HTTPUpstream struct {
	addr      string
	useTLS    bool
	tlsConfig *tls.Config // nil: verify against system roots
	auth      string      // base64 "user:pass", "" when the proxy takes no credentials
	credHint  string      // attached to a credentials rejection; set by ParseUpstreamURL
	label     string
	hosted    bool // interpret the hosted gateway's extra replies
	timeout   time.Duration

	onExitIP func(ip string) // hosted: the gateway reports the exit IP on each tunnel
}

// NewHostedUpstream returns the upstream for a hosted gateway session.
func NewHostedUpstream(addr string, useTLS bool, token string) *HTTPUpstream {
	return &HTTPUpstream{
		addr:    addr,
		useTLS:  useTLS,
		auth:    base64.StdEncoding.EncodeToString([]byte("_:" + token)),
		label:   "shellroute",
		hosted:  true,
		timeout: hostedHandshakeTimeout,
	}
}

// NewHTTPUpstream returns an upstream for a user's own HTTP (useTLS false) or
// HTTPS (useTLS true) proxy. label is shown in status output and must not
// contain the password.
func NewHTTPUpstream(addr string, useTLS bool, username, password, label string) *HTTPUpstream {
	u := &HTTPUpstream{addr: addr, useTLS: useTLS, label: label, timeout: dialTimeout}
	if username != "" || password != "" {
		u.auth = base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	}
	return u
}

// Describe returns the redacted label.
func (u *HTTPUpstream) Describe() string { return u.label }

// ProxyAuthorization returns the header value sent to the proxy, or "".
func (u *HTTPUpstream) ProxyAuthorization() string {
	if u.auth == "" {
		return ""
	}
	return "Basic " + u.auth
}

func (u *HTTPUpstream) setExitIPCallback(f func(string)) { u.onExitIP = f }

// setTLSConfig overrides TLS verification; used by tests with a private CA.
func (u *HTTPUpstream) setTLSConfig(cfg *tls.Config) { u.tlsConfig = cfg }

// DialProxy opens a raw connection to the proxy itself.
func (u *HTTPUpstream) DialProxy(ctx context.Context) (net.Conn, error) {
	conn, err := dialProxyConn(ctx, u.addr, u.useTLS, u.tlsConfig)
	if err != nil {
		return nil, &DialError{Kind: KindDial, Err: err}
	}
	return conn, nil
}

// DialTarget opens a CONNECT tunnel to hostPort through the proxy.
func (u *HTTPUpstream) DialTarget(ctx context.Context, hostPort string) (net.Conn, error) {
	conn, err := u.DialProxy(ctx)
	if err != nil {
		return nil, err
	}

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", hostPort, hostPort)
	if a := u.ProxyAuthorization(); a != "" {
		req += "Proxy-Authorization: " + a + "\r\n"
	}
	req += "\r\n"

	conn.SetDeadline(handshakeDeadline(ctx, u.timeout))
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, &DialError{Kind: KindDial, Err: err}
	}

	// bufio.Reader so tunnel bytes arriving right after the headers are kept.
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, handshakeError("proxy did not answer the CONNECT request", err)
	}
	conn.SetDeadline(time.Time{})

	switch {
	case resp.StatusCode == http.StatusOK:
		if u.hosted && u.onExitIP != nil {
			if ip := resp.Header.Get("X-Exit-IP"); ip != "" {
				u.onExitIP(ip)
			}
		}
		if br.Buffered() > 0 {
			return &bufferedConn{Conn: conn, r: br}, nil
		}
		return conn, nil
	case u.hosted && resp.StatusCode == http.StatusPaymentRequired:
		conn.Close()
		return nil, &DialError{Kind: KindDepleted, Status: resp.StatusCode}
	case resp.StatusCode == http.StatusProxyAuthRequired:
		conn.Close()
		de := &DialError{Kind: KindCredentials, Status: resp.StatusCode, Hint: u.credHint}
		if u.auth == "" {
			de.Msg = "proxy requires a username and password"
		}
		return nil, de
	case u.hosted && resp.Header.Get("X-Route-Dead") == "true":
		conn.Close()
		return nil, &DialError{Kind: KindRouteDead, Status: resp.StatusCode}
	default:
		conn.Close()
		return nil, &DialError{Kind: KindProxyStatus, Status: resp.StatusCode, Msg: resp.Status}
	}
}

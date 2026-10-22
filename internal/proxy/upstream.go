package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// Upstream is where the local proxy sends traffic. The hosted gateway and a
// user's own proxy both implement it. The child process never sees the
// difference: it only ever talks to 127.0.0.1.
type Upstream interface {
	// DialTarget returns a connection that is already tunnelled to hostPort.
	// Failures are returned as *DialError so the server can map them to a
	// client response and a route state.
	DialTarget(ctx context.Context, hostPort string) (net.Conn, error)
	// Describe returns a label for status output. It never contains a password.
	Describe() string
}

// HTTPProxyUpstream is implemented by upstreams that are HTTP proxies
// themselves and can take a plain HTTP request in absolute form directly.
type HTTPProxyUpstream interface {
	Upstream
	// DialProxy opens a raw connection to the proxy itself.
	DialProxy(ctx context.Context) (net.Conn, error)
	// ProxyAuthorization returns the Proxy-Authorization header value, or ""
	// when the proxy takes no credentials.
	ProxyAuthorization() string
}

// OwnFailureThreshold is how many consecutive upstream failures mark a
// user's own proxy dead. The hosted gateway reports death explicitly and
// uses no threshold.
const OwnFailureThreshold = 3

// dialTimeout bounds one connection attempt to a user's own proxy. A
// variable so tests can shorten it.
var dialTimeout = 10 * time.Second

// hostedHandshakeTimeout bounds the CONNECT exchange with the hosted
// gateway, which may build a tunnel on first use and needs longer.
const hostedHandshakeTimeout = 45 * time.Second

// DialErrorKind classifies why an upstream could not reach the target.
type DialErrorKind int

const (
	// KindDial: the proxy itself could not be reached (resolve, refused, timeout).
	KindDial DialErrorKind = iota
	// KindHandshake: the proxy answered, but not with the expected protocol.
	KindHandshake
	// KindCredentials: the proxy rejected the username or password.
	KindCredentials
	// KindProxyStatus: the proxy refused the target. Status carries the HTTP
	// status or the SOCKS reply code, Msg the text to show.
	KindProxyStatus
	// KindDepleted: hosted only. The account balance is used up.
	KindDepleted
	// KindRouteDead: hosted only. The gateway declared the route dead.
	KindRouteDead
)

// DialError reports why an upstream could not open a tunnel.
type DialError struct {
	Kind   DialErrorKind
	Status int
	Msg    string
	Err    error
	Hint   string // what the user can do about it; set for own-proxy credential failures
}

func (e *DialError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("upstream error (kind %d)", e.Kind)
}

func (e *DialError) Unwrap() error { return e.Err }

// Cause returns a short explanation of a dial failure for the user.
func Cause(err error) string {
	var pe *ProbeError
	if errors.As(err, &pe) {
		return pe.Msg
	}
	var de *DialError
	if !errors.As(err, &de) {
		return netCause(err)
	}
	switch de.Kind {
	case KindCredentials:
		if de.Msg != "" {
			return de.Msg
		}
		return "proxy rejected credentials"
	case KindProxyStatus:
		if de.Msg != "" {
			return "proxy returned " + de.Msg
		}
		return fmt.Sprintf("proxy returned %d for CONNECT", de.Status)
	case KindHandshake:
		if de.Msg != "" {
			return de.Msg
		}
		return "proxy did not answer with the expected protocol"
	case KindDepleted:
		return "balance depleted"
	case KindRouteDead:
		return "route is dead"
	default:
		return netCause(de.Err)
	}
}

// Explain is Cause followed by the hint attached to the error, if any.
func Explain(err error) string {
	cause := Cause(err)
	var de *DialError
	if errors.As(err, &de) && de.Hint != "" {
		return cause + ". " + de.Hint
	}
	return cause
}

// netCause names the network reason behind a dial failure.
func netCause(err error) string {
	if err == nil {
		return "connection failed"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "cannot resolve host " + dnsErr.Name
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection refused"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return "timeout"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	return err.Error()
}

// handshakeError classifies a failed protocol exchange with the proxy: a
// timeout is a reachability problem (KindDial), anything else means the
// proxy did not speak the expected protocol (KindHandshake).
func handshakeError(msg string, err error) *DialError {
	var ne net.Error
	if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return &DialError{Kind: KindDial, Err: err}
	}
	return &DialError{Kind: KindHandshake, Msg: msg, Err: err}
}

// TunnelFailed reports whether err means the tunnel could not be opened, as
// opposed to a later step (such as the exit IP service) failing.
func TunnelFailed(err error) bool {
	var de *DialError
	return errors.As(err, &de)
}

// dialProxyConn opens a TCP connection to addr, wrapped in TLS when useTLS is
// set. tlsCfg may be nil for default verification against system roots.
func dialProxyConn(ctx context.Context, addr string, useTLS bool, tlsCfg *tls.Config) (net.Conn, error) {
	d := &net.Dialer{Timeout: dialTimeout}
	if !useTLS {
		return d.DialContext(ctx, "tcp", addr)
	}
	cfg := &tls.Config{}
	if tlsCfg != nil {
		cfg = tlsCfg.Clone()
	}
	if cfg.ServerName == "" {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		cfg.ServerName = host
	}
	td := &tls.Dialer{NetDialer: d, Config: cfg}
	return td.DialContext(ctx, "tcp", addr)
}

// handshakeDeadline is the deadline for a proxy handshake: the context's
// deadline when it is sooner, otherwise now plus timeout.
func handshakeDeadline(ctx context.Context, timeout time.Duration) time.Time {
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		return d
	}
	return deadline
}

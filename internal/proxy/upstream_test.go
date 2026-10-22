package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shellroute/shellroute-cli/internal/meter"
	"github.com/shellroute/shellroute-cli/internal/proxy/proxytest"
)

// roundTrip writes msg through a tunnelled connection to an echo server and
// returns what came back.
func roundTrip(t *testing.T, conn net.Conn, msg string) string {
	t.Helper()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(buf)
}

func dialErr(t *testing.T, err error) *DialError {
	t.Helper()
	var de *DialError
	if !errors.As(err, &de) {
		t.Fatalf("error %v (%T) is not a *DialError", err, err)
	}
	return de
}

// --- HTTP and HTTPS upstreams ---

func TestHTTPUpstream_ConnectRelays(t *testing.T) {
	p := proxytest.StartHTTPProxy(t, "", "")
	echo := proxytest.StartEcho(t)
	up := NewHTTPUpstream(p.Addr, false, "", "", "http://"+p.Addr)

	conn, err := up.DialTarget(context.Background(), echo)
	if err != nil {
		t.Fatalf("DialTarget: %v", err)
	}
	defer conn.Close()
	if got := roundTrip(t, conn, "hello through http"); got != "hello through http" {
		t.Fatalf("echo = %q", got)
	}
	if up.ProxyAuthorization() != "" {
		t.Error("no credentials should mean no Proxy-Authorization")
	}
}

func TestHTTPUpstream_BasicAuthAccepted(t *testing.T) {
	p := proxytest.StartHTTPProxy(t, "alice", "s3cret")
	echo := proxytest.StartEcho(t)
	up := NewHTTPUpstream(p.Addr, false, "alice", "s3cret", "label")

	conn, err := up.DialTarget(context.Background(), echo)
	if err != nil {
		t.Fatalf("DialTarget: %v", err)
	}
	defer conn.Close()
	if got := roundTrip(t, conn, "auth ok"); got != "auth ok" {
		t.Fatalf("echo = %q", got)
	}
	if p.Rejected.Load() != 0 {
		t.Errorf("proxy rejected %d requests", p.Rejected.Load())
	}
}

func TestHTTPUpstream_WrongPasswordIsCredentialsError(t *testing.T) {
	p := proxytest.StartHTTPProxy(t, "alice", "s3cret")
	up := NewHTTPUpstream(p.Addr, false, "alice", "wrong", "label")

	_, err := up.DialTarget(context.Background(), "example.com:443")
	de := dialErr(t, err)
	if de.Kind != KindCredentials {
		t.Fatalf("kind = %d, want KindCredentials", de.Kind)
	}
	if Cause(err) != "proxy rejected credentials" {
		t.Errorf("Cause = %q", Cause(err))
	}
	if p.Rejected.Load() != 1 {
		t.Errorf("proxy rejected %d, want 1", p.Rejected.Load())
	}
}

func TestHTTPUpstream_ProxyStatusForwarded(t *testing.T) {
	// A proxy that refuses every target with 403.
	gwAddr, cleanup := fakeGateway(t, "HTTP/1.1 403 Forbidden\r\n\r\n")
	defer cleanup()
	up := NewHTTPUpstream(gwAddr, false, "", "", "label")

	_, err := up.DialTarget(context.Background(), "example.com:443")
	de := dialErr(t, err)
	if de.Kind != KindProxyStatus || de.Status != 403 {
		t.Fatalf("got kind %d status %d, want KindProxyStatus 403", de.Kind, de.Status)
	}
	if Cause(err) != "proxy returned 403 Forbidden" {
		t.Errorf("Cause = %q", Cause(err))
	}
}

func TestHTTPSUpstream_TLSToProxy(t *testing.T) {
	p, pool := proxytest.StartTLSHTTPProxy(t, "bob", "pw")
	echo := proxytest.StartEcho(t)
	up := NewHTTPUpstream(p.Addr, true, "bob", "pw", "https://bob:***@"+p.Addr)
	up.setTLSConfig(&tls.Config{RootCAs: pool})

	conn, err := up.DialTarget(context.Background(), echo)
	if err != nil {
		t.Fatalf("DialTarget over TLS: %v", err)
	}
	defer conn.Close()
	if got := roundTrip(t, conn, "tls proxy"); got != "tls proxy" {
		t.Fatalf("echo = %q", got)
	}
}

func TestHTTPSUpstream_UntrustedCertificateFails(t *testing.T) {
	p, _ := proxytest.StartTLSHTTPProxy(t, "", "")
	up := NewHTTPUpstream(p.Addr, true, "", "", "label")

	_, err := up.DialTarget(context.Background(), "example.com:443")
	de := dialErr(t, err)
	if de.Kind != KindDial {
		t.Fatalf("kind = %d, want KindDial for a TLS verification failure", de.Kind)
	}
}

func TestHostedUpstream_KeepsGatewaySemantics(t *testing.T) {
	cases := []struct {
		name     string
		response string
		kind     DialErrorKind
	}{
		{"402 depleted", "HTTP/1.1 402 Payment Required\r\n\r\n", KindDepleted},
		{"407 token invalid", "HTTP/1.1 407 Proxy Authentication Required\r\n\r\n", KindCredentials},
		{"X-Route-Dead", "HTTP/1.1 502 Bad Gateway\r\nX-Route-Dead: true\r\n\r\n", KindRouteDead},
		{"5xx transient", "HTTP/1.1 503 Service Unavailable\r\n\r\n", KindProxyStatus},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gwAddr, cleanup := fakeGateway(t, tc.response)
			defer cleanup()
			up := NewHostedUpstream(gwAddr, false, "tok")
			_, err := up.DialTarget(context.Background(), "example.com:443")
			if de := dialErr(t, err); de.Kind != tc.kind {
				t.Errorf("kind = %d, want %d", de.Kind, tc.kind)
			}
		})
	}
}

func TestOwnHTTPUpstream_402IsPlainProxyStatus(t *testing.T) {
	// Only the hosted gateway means "balance depleted" by 402.
	gwAddr, cleanup := fakeGateway(t, "HTTP/1.1 402 Payment Required\r\n\r\n")
	defer cleanup()
	up := NewHTTPUpstream(gwAddr, false, "", "", "label")
	_, err := up.DialTarget(context.Background(), "example.com:443")
	if de := dialErr(t, err); de.Kind != KindProxyStatus {
		t.Errorf("kind = %d, want KindProxyStatus", de.Kind)
	}
}

// --- SOCKS5 upstream ---

func TestSOCKS5Upstream_NoAuthRelays(t *testing.T) {
	s := proxytest.StartSOCKS5(t, "", "")
	echo := proxytest.StartEcho(t)
	up := NewSOCKS5Upstream(s.Addr, "", "", "socks5://"+s.Addr)

	conn, err := up.DialTarget(context.Background(), echo)
	if err != nil {
		t.Fatalf("DialTarget: %v", err)
	}
	defer conn.Close()
	if got := roundTrip(t, conn, "socks hello"); got != "socks hello" {
		t.Fatalf("echo = %q", got)
	}
	if s.Connects.Load() != 1 {
		t.Errorf("connects = %d, want 1", s.Connects.Load())
	}
}

func TestSOCKS5Upstream_DomainNameSentToProxy(t *testing.T) {
	// "localhost" must be resolved by the proxy, not by us.
	s := proxytest.StartSOCKS5(t, "", "")
	echo := proxytest.StartEcho(t)
	_, port, _ := net.SplitHostPort(echo)
	up := NewSOCKS5Upstream(s.Addr, "", "", "label")

	conn, err := up.DialTarget(context.Background(), "localhost:"+port)
	if err != nil {
		t.Fatalf("DialTarget by name: %v", err)
	}
	conn.Close()
}

func TestSOCKS5Upstream_UserPassAccepted(t *testing.T) {
	s := proxytest.StartSOCKS5(t, "carol", "pw")
	echo := proxytest.StartEcho(t)
	up := NewSOCKS5Upstream(s.Addr, "carol", "pw", "label")

	conn, err := up.DialTarget(context.Background(), echo)
	if err != nil {
		t.Fatalf("DialTarget: %v", err)
	}
	defer conn.Close()
	if got := roundTrip(t, conn, "socks auth"); got != "socks auth" {
		t.Fatalf("echo = %q", got)
	}
}

func TestSOCKS5Upstream_WrongPasswordIsCredentialsError(t *testing.T) {
	s := proxytest.StartSOCKS5(t, "carol", "pw")
	up := NewSOCKS5Upstream(s.Addr, "carol", "nope", "label")

	_, err := up.DialTarget(context.Background(), "example.com:443")
	if de := dialErr(t, err); de.Kind != KindCredentials {
		t.Fatalf("kind = %d, want KindCredentials", de.Kind)
	}
	if Cause(err) != "proxy rejected credentials" {
		t.Errorf("Cause = %q", Cause(err))
	}
}

func TestSOCKS5Upstream_AuthRequiredButNoneGiven(t *testing.T) {
	s := proxytest.StartSOCKS5(t, "carol", "pw")
	up := NewSOCKS5Upstream(s.Addr, "", "", "label")

	_, err := up.DialTarget(context.Background(), "example.com:443")
	de := dialErr(t, err)
	if de.Kind != KindCredentials {
		t.Fatalf("kind = %d, want KindCredentials", de.Kind)
	}
	if !strings.Contains(Cause(err), "requires a username and password") {
		t.Errorf("Cause = %q", Cause(err))
	}
}

func TestSOCKS5Upstream_TargetRefusedIsProxyStatus(t *testing.T) {
	s := proxytest.StartSOCKS5(t, "", "")
	up := NewSOCKS5Upstream(s.Addr, "", "", "label")

	_, err := up.DialTarget(context.Background(), "127.0.0.1:1")
	de := dialErr(t, err)
	if de.Kind != KindProxyStatus {
		t.Fatalf("kind = %d, want KindProxyStatus", de.Kind)
	}
	if Cause(err) != "proxy returned connection refused" {
		t.Errorf("Cause = %q", Cause(err))
	}
}

func TestSOCKS5Upstream_RulesetRefusalAfterAuthHintsAtPassword(t *testing.T) {
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
				buf := make([]byte, 64)
				c.Read(buf)                                               // greeting
				c.Write([]byte{0x05, 0x02})                               // choose user/pass
				c.Read(buf)                                               // credentials
				c.Write([]byte{0x01, 0x00})                               // "accepted"
				c.Read(buf)                                               // CONNECT
				c.Write([]byte{0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // not allowed by ruleset
			}(conn)
		}
	}()
	up := NewSOCKS5Upstream(ln.Addr().String(), "alice", "wrong", "label")
	_, err = up.DialTarget(context.Background(), "example.com:443")
	de := dialErr(t, err)
	if de.Kind != KindProxyStatus || de.Status != 2 {
		t.Fatalf("kind %d status %d", de.Kind, de.Status)
	}
	if !strings.Contains(Cause(err), "check the username and password") {
		t.Errorf("Cause = %q", Cause(err))
	}
}

func TestSOCKS5Upstream_NotASOCKSServer(t *testing.T) {
	// A server that answers every byte stream with HTTP text.
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
				buf := make([]byte, 64)
				c.Read(buf)
				io.WriteString(c, "HTTP/1.1 400 Bad Request\r\n\r\n")
			}(conn)
		}
	}()
	up := NewSOCKS5Upstream(ln.Addr().String(), "", "", "label")

	_, err = up.DialTarget(context.Background(), "example.com:443")
	if de := dialErr(t, err); de.Kind != KindHandshake {
		t.Fatalf("kind = %d, want KindHandshake", de.Kind)
	}
	if Cause(err) != "not a SOCKS5 proxy" {
		t.Errorf("Cause = %q", Cause(err))
	}
}

// --- Network failures and causes ---

func TestUpstream_ConnectionRefused(t *testing.T) {
	up := NewHTTPUpstream("127.0.0.1:1", false, "", "", "label")
	_, err := up.DialTarget(context.Background(), "example.com:443")
	if de := dialErr(t, err); de.Kind != KindDial {
		t.Fatalf("kind = %d, want KindDial", de.Kind)
	}
	if Cause(err) != "connection refused" {
		t.Errorf("Cause = %q", Cause(err))
	}
}

func TestUpstream_TimeoutIsBounded(t *testing.T) {
	// A proxy that accepts and never answers.
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
			defer conn.Close()
		}
	}()

	old := dialTimeout
	dialTimeout = 300 * time.Millisecond
	defer func() { dialTimeout = old }()

	for _, up := range []Upstream{
		NewHTTPUpstream(ln.Addr().String(), false, "", "", "http"),
		NewSOCKS5Upstream(ln.Addr().String(), "", "", "socks5"),
	} {
		start := time.Now()
		_, err := up.DialTarget(context.Background(), "example.com:443")
		if err == nil {
			t.Fatalf("%s: expected an error", up.Describe())
		}
		if Cause(err) != "timeout" {
			t.Errorf("%s: Cause = %q, want timeout", up.Describe(), Cause(err))
		}
		if d := time.Since(start); d > 4*dialTimeout {
			t.Errorf("%s: took %v, want about %v", up.Describe(), d, dialTimeout)
		}
	}
}

func TestCause_NamesDNSFailure(t *testing.T) {
	err := &DialError{Kind: KindDial, Err: &net.DNSError{Name: "proxy.example", IsNotFound: true}}
	if Cause(err) != "cannot resolve host proxy.example" {
		t.Errorf("Cause = %q", Cause(err))
	}
}

func TestCause_PlainErrorPassesThrough(t *testing.T) {
	if Cause(errors.New("boom")) != "boom" {
		t.Error("non-DialError should pass through")
	}
	if TunnelFailed(errors.New("boom")) {
		t.Error("plain error is not a tunnel failure")
	}
	if !TunnelFailed(&DialError{Kind: KindDial}) {
		t.Error("DialError is a tunnel failure")
	}
}

// --- URL parsing ---

func TestParseUpstreamURL(t *testing.T) {
	cases := []struct {
		raw      string
		wantType string
		wantErr  string
	}{
		{"http://user:pass@proxy.example:8080", "*proxy.HTTPUpstream", ""},
		{"HTTPS://proxy.example:443", "*proxy.HTTPUpstream", ""},
		{"socks5://proxy.example:1080", "*proxy.SOCKS5Upstream", ""},
		{"socks5h://u:p@[::1]:1080", "*proxy.SOCKS5Upstream", ""},
		{"", "", "empty"},
		{"proxy.example:8080", "", "needs a scheme"},
		{"ftp://proxy.example:21", "", "unsupported proxy scheme"},
		{"http://proxy.example", "", "needs a port"},
		{"http://:8080", "", "needs a host"},
		{"http://proxy.example:8080/path", "", "must not have a path"},
		{"http://proxy.example:8080?x=1", "", "must not have a path"},
		{"http://user:pa ss@host:1", "*proxy.HTTPUpstream", ""},
		{"http://host:1 x", "", "not valid"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			up, err := ParseUpstreamURL(tc.raw)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := typeName(up); got != tc.wantType {
				t.Errorf("type = %s, want %s", got, tc.wantType)
			}
		})
	}
}

func TestParseSavedUpstreamURL_LegacyDecodesCredentialsAndRedactsLabel(t *testing.T) {
	up, err := ParseSavedUpstreamURL("http://al%40ice:p%3Ass@proxy.example:8080", false)
	if err != nil {
		t.Fatal(err)
	}
	h := up.(*HTTPUpstream)
	if h.addr != "proxy.example:8080" {
		t.Errorf("addr = %q", h.addr)
	}
	if h.ProxyAuthorization() != "Basic YWxAaWNlOnA6c3M=" { // base64("al@ice:p:ss")
		t.Errorf("auth = %q", h.ProxyAuthorization())
	}
	if strings.Contains(up.Describe(), "ss") || !strings.Contains(up.Describe(), "***") {
		t.Errorf("Describe leaks the password: %q", up.Describe())
	}
}

func TestParseUpstreamURL_ErrorsNeverContainPassword(t *testing.T) {
	for _, raw := range []string{
		"ftp://user:topsecret@h:1",
		"http://user:topsecret@h",
		"http://user:topsecret@h:1/path",
	} {
		_, err := ParseUpstreamURL(raw)
		if err == nil {
			t.Fatalf("%s: expected error", raw)
		}
		if strings.Contains(err.Error(), "topsecret") {
			t.Errorf("error leaks password: %v", err)
		}
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"http://user:secret@h:8080":  "http://user:***@h:8080",
		"socks5://h:1080":            "socks5://h:1080",
		"http://user@h:8080":         "http://user@h:8080",
		"https://u:p@[::1]:443":      "https://u:***@[::1]:443",
		"not a url":                  "(invalid proxy URL)",
		"http://user:se%40cret@h:80": "http://user:***@h:80",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- Origin-form rewrite for SOCKS5 plain HTTP ---

func TestOriginForm(t *testing.T) {
	in := []byte("GET http://example.com/path?q=1 HTTP/1.1\r\nHost: example.com\r\nProxy-Connection: keep-alive\r\nUser-Agent: curl\r\n\r\nbody")
	host, out, err := originForm(in)
	if err != nil {
		t.Fatal(err)
	}
	if host != "example.com:80" {
		t.Errorf("host = %q", host)
	}
	want := "GET /path?q=1 HTTP/1.1\r\nHost: example.com\r\nUser-Agent: curl\r\n\r\nbody"
	if string(out) != want {
		t.Errorf("out = %q\nwant %q", out, want)
	}
}

func TestOriginForm_PortAndMissingHostHeader(t *testing.T) {
	host, out, err := originForm([]byte("GET http://example.com:8081 HTTP/1.0\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if host != "example.com:8081" {
		t.Errorf("host = %q", host)
	}
	if !strings.HasPrefix(string(out), "GET / HTTP/1.0\r\nHost: example.com:8081\r\n") {
		t.Errorf("out = %q", out)
	}
}

func TestOriginForm_OriginFormInputUsesHostHeader(t *testing.T) {
	host, out, err := originForm([]byte("POST /submit HTTP/1.1\r\nHost: api.example:9000\r\nContent-Length: 2\r\n\r\nhi"))
	if err != nil {
		t.Fatal(err)
	}
	if host != "api.example:9000" {
		t.Errorf("host = %q", host)
	}
	if !strings.HasSuffix(string(out), "\r\n\r\nhi") {
		t.Errorf("body lost: %q", out)
	}
}

func TestOriginForm_Rejects(t *testing.T) {
	for _, in := range []string{"GET /x HTTP/1.1\r\n\r\n", "GARBAGE\r\n\r\n", "GET http://x/ HTTP/1.1\r\nHost: x"} {
		if _, _, err := originForm([]byte(in)); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

// --- Server over own upstreams ---

// freePort reserves a loopback port for a Server, as session.Start does.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// startServer runs s until the test ends and returns once it accepts connections.
func startServer(t *testing.T, s *Server) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.Start(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", s.listenAddr, 100*time.Millisecond); err == nil {
			c.Close()
			return s.listenAddr
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return ""
}

func viaLocalProxy(t *testing.T, proxyAddr string, tlsCfg *tls.Config) *http.Client {
	t.Helper()
	pu, _ := neturl.Parse("http://" + proxyAddr)
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(pu),
			TLSClientConfig: tlsCfg,
		},
	}
}

func TestServer_HTTPSViaConnectThroughEachUpstream(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "secure hello")
	}))
	defer target.Close()
	tlsCfg := &tls.Config{RootCAs: certPoolFor(t, target)}

	hp := proxytest.StartHTTPProxy(t, "u", "p")
	sp := proxytest.StartSOCKS5(t, "u", "p")
	for name, up := range map[string]Upstream{
		"http":   NewHTTPUpstream(hp.Addr, false, "u", "p", "http"),
		"socks5": NewSOCKS5Upstream(sp.Addr, "u", "p", "socks5"),
	} {
		t.Run(name, func(t *testing.T) {
			s := NewServer(&Config{ListenPort: freePort(t), Upstream: up, Meter: meter.New(), FailureThreshold: OwnFailureThreshold})
			addr := startServer(t, s)
			resp, err := viaLocalProxy(t, addr, tlsCfg).Get(target.URL)
			if err != nil {
				t.Fatalf("GET through local proxy: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "secure hello" {
				t.Errorf("body = %q", body)
			}
			if s.BytesTotal() == 0 {
				t.Error("relay metered no bytes")
			}
		})
	}
}

func TestServer_PlainHTTPThroughEachUpstream(t *testing.T) {
	var gotPath, gotHost atomic.Value
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.RequestURI())
		gotHost.Store(r.Host)
		io.WriteString(w, "plain hello")
	}))
	defer target.Close()

	hp := proxytest.StartHTTPProxy(t, "", "")
	sp := proxytest.StartSOCKS5(t, "", "")
	for name, up := range map[string]Upstream{
		"http":   NewHTTPUpstream(hp.Addr, false, "", "", "http"),
		"socks5": NewSOCKS5Upstream(sp.Addr, "", "", "socks5"),
	} {
		t.Run(name, func(t *testing.T) {
			s := NewServer(&Config{ListenPort: freePort(t), Upstream: up, Meter: meter.New(), FailureThreshold: OwnFailureThreshold})
			addr := startServer(t, s)
			resp, err := viaLocalProxy(t, addr, nil).Get(target.URL + "/hello?x=1")
			if err != nil {
				t.Fatalf("GET through local proxy: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "plain hello" {
				t.Errorf("body = %q", body)
			}
			if gotPath.Load() != "/hello?x=1" {
				t.Errorf("target saw path %v, want /hello?x=1 (origin form)", gotPath.Load())
			}
			if gotHost.Load() != strings.TrimPrefix(target.URL, "http://") {
				t.Errorf("target saw Host %v", gotHost.Load())
			}
		})
	}
}

// scriptedUpstream returns the queued results in order.
type scriptedUpstream struct {
	mu    sync.Mutex
	errs  []error
	calls int
}

func (u *scriptedUpstream) DialTarget(ctx context.Context, hostPort string) (net.Conn, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	var err error
	if u.calls < len(u.errs) {
		err = u.errs[u.calls]
	}
	u.calls++
	if err != nil {
		return nil, err
	}
	a, b := net.Pipe()
	go func() { io.Copy(io.Discard, b); b.Close() }()
	return a, nil
}

func (u *scriptedUpstream) Describe() string { return "scripted" }

func drainedPipe(t *testing.T) (client, server net.Conn) {
	t.Helper()
	client, server = net.Pipe()
	go io.Copy(io.Discard, client)
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}

func TestServer_OwnUpstreamDeadAfterConsecutiveFailures(t *testing.T) {
	refused := &DialError{Kind: KindDial, Err: errors.New("refused")}
	up := &scriptedUpstream{errs: []error{refused, refused, refused}}
	var reasons []string
	var mu sync.Mutex
	s := NewServer(&Config{Upstream: up, Meter: meter.New(), FailureThreshold: 3})
	s.OnUpstreamDead = func(r string) { mu.Lock(); reasons = append(reasons, r); mu.Unlock() }

	for i := 0; i < 2; i++ {
		_, server := drainedPipe(t)
		s.dialAndSendConnect(context.Background(), server, "x:443")
		if s.RouteState() != RouteHealthy {
			t.Fatalf("after %d failures state = %d, want healthy", i+1, s.RouteState())
		}
	}
	_, server := drainedPipe(t)
	s.dialAndSendConnect(context.Background(), server, "x:443")
	if s.RouteState() != RouteDead {
		t.Fatalf("after 3 failures state = %d, want RouteDead", s.RouteState())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reasons) != 1 || reasons[0] != "connection_failed" {
		t.Errorf("OnUpstreamDead reasons = %v, want [connection_failed]", reasons)
	}
}

func TestServer_OwnUpstreamSuccessResetsFailureCount(t *testing.T) {
	refused := &DialError{Kind: KindDial, Err: errors.New("refused")}
	up := &scriptedUpstream{errs: []error{refused, refused, nil, refused, refused}}
	s := NewServer(&Config{Upstream: up, Meter: meter.New(), FailureThreshold: 3})

	for i := 0; i < 5; i++ {
		_, server := drainedPipe(t)
		if c := s.dialAndSendConnect(context.Background(), server, "x:443"); c != nil {
			c.Close()
		}
	}
	if s.RouteState() != RouteHealthy {
		t.Fatalf("state = %d, want healthy: a success must reset the counter", s.RouteState())
	}
	if s.failures.Load() != 2 {
		t.Errorf("failures = %d, want 2", s.failures.Load())
	}
}

func TestServer_OwnUpstreamCredentialsErrorIsImmediatelyDead(t *testing.T) {
	up := &scriptedUpstream{errs: []error{&DialError{Kind: KindCredentials}}}
	var reason string
	s := NewServer(&Config{Upstream: up, Meter: meter.New(), FailureThreshold: 3})
	s.OnUpstreamDead = func(r string) { reason = r }

	client, server := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() { s.dialAndSendConnect(context.Background(), server, "x:443"); server.Close(); close(done) }()
	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil || resp.StatusCode != 407 {
		t.Fatalf("client got %v %v, want 407", resp, err)
	}
	<-done
	if s.RouteState() != RouteDead {
		t.Errorf("state = %d, want RouteDead", s.RouteState())
	}
	if reason != "auth_invalid" {
		t.Errorf("reason = %q, want auth_invalid", reason)
	}
}

func TestServer_HostedUpstreamNeverCountsFailures(t *testing.T) {
	refused := &DialError{Kind: KindDial, Err: errors.New("refused")}
	up := &scriptedUpstream{errs: []error{refused, refused, refused, refused}}
	s := NewServer(&Config{Upstream: up, Meter: meter.New()}) // threshold 0: hosted semantics
	for i := 0; i < 4; i++ {
		_, server := drainedPipe(t)
		s.dialAndSendConnect(context.Background(), server, "x:443")
	}
	if s.RouteState() != RouteHealthy {
		t.Errorf("state = %d, want healthy: hosted dial failures are transient", s.RouteState())
	}
}

func TestServer_SOCKSRulesetRefusalIs403NotFailure(t *testing.T) {
	up := &scriptedUpstream{errs: []error{&DialError{Kind: KindProxyStatus, Status: 2, Msg: "connection not allowed by ruleset"}}}
	s := NewServer(&Config{Upstream: up, Meter: meter.New(), FailureThreshold: 1})
	client, server := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() { s.dialAndSendConnect(context.Background(), server, "x:443"); server.Close(); close(done) }()
	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil || resp.StatusCode != 403 {
		t.Fatalf("client got %v %v, want 403", resp, err)
	}
	<-done
	if s.RouteState() != RouteHealthy {
		t.Error("a per-target refusal must not mark the route dead")
	}
}

func TestServer_SetUpstreamResetsState(t *testing.T) {
	s := NewServer(&Config{Upstream: &scriptedUpstream{}, Meter: meter.New(), FailureThreshold: 3})
	s.failures.Store(2)
	s.SetRouteDead(false)
	s.SetUpstream(&scriptedUpstream{})
	if s.RouteState() != RouteHealthy || s.failures.Load() != 0 {
		t.Error("SetUpstream must reset route state and failure count")
	}
}

// --- Probe and Check ---

// directUpstream dials the target itself; it stands in for a working proxy.
type directUpstream struct{ calls atomic.Int32 }

func (d *directUpstream) DialTarget(ctx context.Context, hostPort string) (net.Conn, error) {
	d.calls.Add(1)
	return (&net.Dialer{}).DialContext(ctx, "tcp", hostPort)
}
func (d *directUpstream) Describe() string { return "direct" }

func withExitIPEndpoints(t *testing.T, endpoints ...string) {
	t.Helper()
	old := ExitInfoEndpoints
	ExitInfoEndpoints = endpoints
	t.Cleanup(func() { ExitInfoEndpoints = old })
}

func TestProbe_ReadsCountryFromJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ip": "203.0.113.7", "city": "Vilnius", "country": "lt"}`)
	}))
	defer ts.Close()
	withExitIPEndpoints(t, ts.URL)

	res, err := Probe(context.Background(), &directUpstream{}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitIP != "203.0.113.7" || res.Country != "LT" {
		t.Errorf("res = %+v, want IP 203.0.113.7 and country LT", res)
	}
}

func TestParseExitInfo(t *testing.T) {
	cases := []struct{ body, ip, country string }{
		{`{"ip":"1.2.3.4","country":"DE"}`, "1.2.3.4", "DE"},
		{`{"ip":"1.2.3.4"}`, "1.2.3.4", ""},
		{"1.2.3.4\n", "1.2.3.4", ""},
		{`{"ip":"not-an-ip"}`, "", ""},
		{"<html>", "", ""},
		{`{"country":"DE"}`, "", ""},
	}
	for _, tc := range cases {
		ip, country := parseExitInfo([]byte(tc.body))
		if ip != tc.ip || country != tc.country {
			t.Errorf("parseExitInfo(%q) = %q,%q want %q,%q", tc.body, ip, country, tc.ip, tc.country)
		}
	}
}

func TestProbe_ReadsExitIPThroughUpstream(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "203.0.113.7\n")
	}))
	defer ts.Close()
	withExitIPEndpoints(t, ts.URL)

	res, err := Probe(context.Background(), &directUpstream{}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitIP != "203.0.113.7" {
		t.Errorf("ExitIP = %q", res.ExitIP)
	}
	if res.Latency <= 0 {
		t.Error("latency should be measured")
	}
}

func TestProbe_FallsBackToSecondEndpoint(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "<html>") }))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "198.51.100.2") }))
	defer good.Close()
	withExitIPEndpoints(t, bad.URL, good.URL)

	res, err := Probe(context.Background(), &directUpstream{}, 5*time.Second)
	if err != nil || res.ExitIP != "198.51.100.2" {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestProbe_TunnelOKButNoIPIsNotTunnelFailure(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "nope") }))
	defer bad.Close()
	withExitIPEndpoints(t, bad.URL)

	_, err := Check(context.Background(), &directUpstream{})
	if err == nil {
		t.Fatal("expected error")
	}
	if TunnelFailed(err) {
		t.Error("a bad echo reply is not a tunnel failure")
	}
}

func TestProbe_FailureTextIsPlain(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html>busy</html>")
	}))
	defer bad.Close()
	withExitIPEndpoints(t, bad.URL)
	_, err := Probe(context.Background(), &directUpstream{}, 5*time.Second)
	if err == nil || err.Error() != "the exit IP service gave an unexpected reply" {
		t.Fatalf("unexpected reply: err = %v", err)
	}
	if TunnelFailed(err) || Cause(err) != err.Error() {
		t.Errorf("a probe failure is not a tunnel failure; Cause = %q", Cause(err))
	}

	block := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer slow.Close()
	defer close(block)
	withExitIPEndpoints(t, slow.URL)
	_, err = Probe(context.Background(), &directUpstream{}, 200*time.Millisecond)
	if err == nil || err.Error() != "the exit IP service did not answer in time" {
		t.Fatalf("slow service: err = %v", err)
	}
	for _, leak := range []string{"http", "context deadline", "Get "} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("raw error text %q shown to the user: %q", leak, err)
		}
	}
}

func TestCheck_RetriesDialFailureOnce(t *testing.T) {
	withExitIPEndpoints(t, "http://127.0.0.1:1/ip")
	refused := &DialError{Kind: KindDial, Err: errors.New("refused")}
	up := &scriptedUpstream{errs: []error{refused, refused, refused}}

	_, err := Check(context.Background(), up)
	if !TunnelFailed(err) {
		t.Fatalf("err = %v, want tunnel failure", err)
	}
	if up.calls != 2 {
		t.Errorf("DialTarget called %d times, want 2 (one attempt, one retry)", up.calls)
	}
}

func TestCheck_DoesNotRetryCredentials(t *testing.T) {
	withExitIPEndpoints(t, "http://127.0.0.1:1/ip")
	up := &scriptedUpstream{errs: []error{&DialError{Kind: KindCredentials}, &DialError{Kind: KindCredentials}}}

	_, err := Check(context.Background(), up)
	if de := dialErr(t, err); de.Kind != KindCredentials {
		t.Fatalf("kind = %d", de.Kind)
	}
	if up.calls != 1 {
		t.Errorf("DialTarget called %d times, want 1", up.calls)
	}
}

func certPoolFor(t *testing.T, ts *httptest.Server) *x509.CertPool {
	t.Helper()
	return ts.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
}

func typeName(v interface{}) string {
	switch v.(type) {
	case *HTTPUpstream:
		return "*proxy.HTTPUpstream"
	case *SOCKS5Upstream:
		return "*proxy.SOCKS5Upstream"
	}
	return "?"
}

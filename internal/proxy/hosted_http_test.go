package proxy

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"

	"github.com/shellroute/shellroute-cli/internal/meter"
)

// tunnelRequest is a request the fake gateway received inside a tunnel.
type tunnelRequest struct {
	tunnel     string // CONNECT target of the tunnel it arrived on
	requestURI string
	host       string
	close      bool
	proxyAuth  string
}

// connectOnlyGateway imitates the hosted gateway: every method except CONNECT
// gets 405. Inside a tunnel it plays the target web server. It answers each
// request with the tunnel's CONNECT target and, like a keep-alive server,
// keeps the connection open unless the request asked to close it.
type connectOnlyGateway struct {
	addr string

	mu       sync.Mutex
	connects []string // CONNECT targets, in order
	auths    []string // Proxy-Authorization of each CONNECT
	requests []tunnelRequest
	refused  int // non-CONNECT requests answered with 405
}

func startConnectOnlyGateway(t *testing.T) *connectOnlyGateway {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	g := &connectOnlyGateway{addr: ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go g.handle(c)
		}
	}()
	return g
}

func (g *connectOnlyGateway) handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method != http.MethodConnect {
		g.mu.Lock()
		g.refused++
		g.mu.Unlock()
		io.WriteString(c, "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n")
		return
	}
	g.mu.Lock()
	g.connects = append(g.connects, req.Host)
	g.auths = append(g.auths, req.Header.Get("Proxy-Authorization"))
	g.mu.Unlock()
	io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")

	for {
		in, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		io.Copy(io.Discard, in.Body)
		g.mu.Lock()
		g.requests = append(g.requests, tunnelRequest{
			tunnel:     req.Host,
			requestURI: in.RequestURI,
			host:       in.Host,
			close:      in.Close,
			proxyAuth:  in.Header.Get("Proxy-Authorization"),
		})
		g.mu.Unlock()

		resp := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n", len(req.Host))
		if in.Close {
			resp += "Connection: close\r\n"
		}
		io.WriteString(c, resp+"\r\n"+req.Host)
		if in.Close {
			return
		}
	}
}

func startHostedServer(t *testing.T, gwAddr string) (*Server, string) {
	t.Helper()
	s := NewServer(&Config{
		ListenPort: freePort(t),
		Upstream:   NewHostedUpstream(gwAddr, false, "test-token"),
		Meter:      meter.New(),
	})
	return s, startServer(t, s)
}

func getBody(t *testing.T, client *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s through local proxy: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// The hosted gateway accepts only CONNECT. A plain HTTP request must reach
// it as a tunnel to port 80 carrying the request in origin form.
func TestServer_HostedPlainHTTPGoesThroughConnect(t *testing.T) {
	g := startConnectOnlyGateway(t)
	s, addr := startHostedServer(t, g.addr)

	status, body := getBody(t, viaLocalProxy(t, addr, nil), "http://example.test/path?q=1")
	if status != http.StatusOK || body != "example.test:80" {
		t.Fatalf("got %d %q, want 200 from a tunnel to example.test:80", status, body)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.refused != 0 {
		t.Errorf("gateway refused %d requests that were not CONNECT", g.refused)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("_:test-token"))
	if len(g.connects) != 1 || g.connects[0] != "example.test:80" || g.auths[0] != wantAuth {
		t.Errorf("CONNECTs %v with auth %v, want one to example.test:80 with the session token", g.connects, g.auths)
	}
	if len(g.requests) != 1 {
		t.Fatalf("requests inside tunnels = %d, want 1", len(g.requests))
	}
	r := g.requests[0]
	if r.requestURI != "/path?q=1" || r.host != "example.test" {
		t.Errorf("target saw %q with Host %q, want origin form /path?q=1 with Host example.test", r.requestURI, r.host)
	}
	if !r.close {
		t.Error("request did not ask the target to close after its response")
	}
	if r.proxyAuth != "" {
		t.Error("proxy credentials reached the target")
	}
	if s.BytesTotal() == 0 {
		t.Error("relay metered no bytes")
	}
}

// A client may reuse its connection to the local proxy for a plain HTTP
// request to another host. Each request must still reach its own host.
func TestServer_HostedPlainHTTPToTwoHostsUsesTwoTunnels(t *testing.T) {
	g := startConnectOnlyGateway(t)
	_, addr := startHostedServer(t, g.addr)
	client := viaLocalProxy(t, addr, nil)

	for _, host := range []string{"a.test", "b.test"} {
		if _, body := getBody(t, client, "http://"+host+"/"); body != host+":80" {
			t.Errorf("request for %s was answered through the tunnel to %q", host, body)
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.connects) != 2 {
		t.Errorf("CONNECTs = %v, want one per host", g.connects)
	}
}

// A user's own HTTP proxy still gets a plain HTTP request as it is: in
// absolute form, with its credentials, and with the client's connection
// handling.
func TestServer_OwnHTTPProxyStillGetsAbsoluteForm(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan *http.Request, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		req, err := http.ReadRequest(bufio.NewReader(c))
		if err != nil {
			return
		}
		got <- req
		io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
	}()

	s := NewServer(&Config{
		ListenPort:       freePort(t),
		Upstream:         NewHTTPUpstream(ln.Addr().String(), false, "u", "p", "own"),
		Meter:            meter.New(),
		FailureThreshold: OwnFailureThreshold,
	})
	addr := startServer(t, s)

	if status, body := getBody(t, viaLocalProxy(t, addr, nil), "http://example.test/x"); status != http.StatusOK || body != "ok" {
		t.Fatalf("got %d %q, want 200 ok", status, body)
	}
	req := <-got
	if req.Method != http.MethodGet || req.RequestURI != "http://example.test/x" {
		t.Errorf("own proxy got %s %s, want GET http://example.test/x", req.Method, req.RequestURI)
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("u:p")); req.Header.Get("Proxy-Authorization") != want {
		t.Errorf("own proxy got Proxy-Authorization %q, want %q", req.Header.Get("Proxy-Authorization"), want)
	}
	if req.Close {
		t.Error("request to the own proxy was changed to close the connection")
	}
}

func TestCloseAfterResponse(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"adds the header",
			"GET / HTTP/1.1\r\nHost: a\r\n\r\n",
			"GET / HTTP/1.1\r\nHost: a\r\nConnection: close\r\n\r\n",
		},
		{
			"replaces keep-alive and keeps the body",
			"POST / HTTP/1.1\r\nHost: a\r\nConnection: keep-alive\r\nKeep-Alive: timeout=5\r\nContent-Length: 2\r\n\r\nhi",
			"POST / HTTP/1.1\r\nHost: a\r\nContent-Length: 2\r\nConnection: close\r\n\r\nhi",
		},
		{
			"leaves an upgrade request alone",
			"GET /ws HTTP/1.1\r\nHost: a\r\nConnection: keep-alive, Upgrade\r\nUpgrade: websocket\r\n\r\n",
			"GET /ws HTTP/1.1\r\nHost: a\r\nConnection: keep-alive, Upgrade\r\nUpgrade: websocket\r\n\r\n",
		},
		{
			"leaves an incomplete head alone",
			"GET / HTTP/1.1\r\nHost: a",
			"GET / HTTP/1.1\r\nHost: a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(closeAfterResponse([]byte(tc.in))); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

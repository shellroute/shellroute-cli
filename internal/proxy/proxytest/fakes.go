// Package proxytest provides in-process proxy servers for tests: a forward
// HTTP proxy with optional basic auth (plain or TLS) and a SOCKS5 server with
// optional username/password auth. Everything stays on loopback.
package proxytest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"io"
	"math/big"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// HTTPProxy is a forward HTTP proxy. It answers CONNECT with a tunnel and
// forwards absolute-form requests to their target.
type HTTPProxy struct {
	Addr     string
	Requests atomic.Int32 // requests that passed authentication
	Rejected atomic.Int32 // requests answered with 407

	ln   net.Listener
	auth string // expected Proxy-Authorization value, "" for none
}

// StartHTTPProxy starts a plain HTTP proxy. Empty user and pass disable auth.
func StartHTTPProxy(t testing.TB, user, pass string) *HTTPProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return startHTTPProxy(t, ln, user, pass)
}

// StartTLSHTTPProxy starts an HTTPS proxy (TLS to the proxy itself) with a
// self-signed certificate and returns the pool that trusts it.
func StartTLSHTTPProxy(t testing.TB, user, pass string) (*HTTPProxy, *x509.CertPool) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cert, pool := selfSigned(t)
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	return startHTTPProxy(t, tlsLn, user, pass), pool
}

func startHTTPProxy(t testing.TB, ln net.Listener, user, pass string) *HTTPProxy {
	p := &HTTPProxy{Addr: ln.Addr().String(), ln: ln}
	if user != "" || pass != "" {
		p.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go p.handle(conn)
		}
	}()
	return p
}

func (p *HTTPProxy) handle(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if p.auth != "" && req.Header.Get("Proxy-Authorization") != p.auth {
		p.Rejected.Add(1)
		io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"test\"\r\nContent-Length: 0\r\n\r\n")
		return
	}
	p.Requests.Add(1)

	if req.Method == http.MethodConnect {
		target, err := net.DialTimeout("tcp", req.Host, 5*time.Second)
		if err != nil {
			io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			return
		}
		defer target.Close()
		io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		relay(conn, br, target)
		return
	}

	// Absolute-form request: forward it to the origin.
	req.RequestURI = ""
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("Proxy-Connection")
	tr := &http.Transport{Proxy: nil}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer resp.Body.Close()
	resp.Write(conn)
}

// SOCKS5Server is a SOCKS5 proxy with optional username/password auth.
type SOCKS5Server struct {
	Addr     string
	Connects atomic.Int32 // successful CONNECT commands
	Rejected atomic.Int32 // failed authentications

	ln   net.Listener
	user string
	pass string
}

// StartSOCKS5 starts a SOCKS5 server. Empty user and pass disable auth.
func StartSOCKS5(t testing.TB, user, pass string) *SOCKS5Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &SOCKS5Server{Addr: ln.Addr().String(), ln: ln, user: user, pass: pass}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(conn)
		}
	}()
	return s
}

func (s *SOCKS5Server) handle(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	var head [2]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil || head[0] != 0x05 {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	needAuth := s.user != "" || s.pass != ""
	if needAuth {
		offered := false
		for _, m := range methods {
			if m == 0x02 {
				offered = true
			}
		}
		if !offered {
			conn.Write([]byte{0x05, 0xFF})
			return
		}
		conn.Write([]byte{0x05, 0x02})
		var ah [2]byte
		if _, err := io.ReadFull(conn, ah[:]); err != nil || ah[0] != 0x01 {
			return
		}
		user := make([]byte, int(ah[1]))
		if _, err := io.ReadFull(conn, user); err != nil {
			return
		}
		var pl [1]byte
		if _, err := io.ReadFull(conn, pl[:]); err != nil {
			return
		}
		pass := make([]byte, int(pl[0]))
		if _, err := io.ReadFull(conn, pass); err != nil {
			return
		}
		if string(user) != s.user || string(pass) != s.pass {
			s.Rejected.Add(1)
			conn.Write([]byte{0x01, 0x01})
			return
		}
		conn.Write([]byte{0x01, 0x00})
	} else {
		conn.Write([]byte{0x05, 0x00})
	}

	var req [4]byte
	if _, err := io.ReadFull(conn, req[:]); err != nil || req[0] != 0x05 {
		return
	}
	var host string
	switch req[3] {
	case 0x01:
		var ip [4]byte
		if _, err := io.ReadFull(conn, ip[:]); err != nil {
			return
		}
		host = net.IP(ip[:]).String()
	case 0x04:
		var ip [16]byte
		if _, err := io.ReadFull(conn, ip[:]); err != nil {
			return
		}
		host = net.IP(ip[:]).String()
	case 0x03:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			return
		}
		name := make([]byte, int(l[0]))
		if _, err := io.ReadFull(conn, name); err != nil {
			return
		}
		host = string(name)
	default:
		conn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	var portB [2]byte
	if _, err := io.ReadFull(conn, portB[:]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portB[:])
	if req[1] != 0x01 {
		conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	target, err := net.DialTimeout("tcp", net.JoinHostPort(host, itoa(int(port))), 5*time.Second)
	if err != nil {
		conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()
	s.Connects.Add(1)
	conn.SetDeadline(time.Time{})
	conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	relay(conn, nil, target)
}

// StartEcho starts a TCP server that writes back whatever it reads.
func StartEcho(t testing.TB) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// relay copies bytes both ways until one side closes. buffered, when not
// nil, holds client bytes already read ahead of the raw connection.
func relay(client net.Conn, buffered *bufio.Reader, target net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		var src io.Reader = client
		if buffered != nil {
			src = buffered
		}
		io.Copy(target, src)
		if tc, ok := target.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		io.Copy(client, target)
		if tc, ok := client.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()
	wg.Wait()
}

func selfSigned(t testing.TB) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "proxytest"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

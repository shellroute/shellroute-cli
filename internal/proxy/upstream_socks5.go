package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	socksVersion      = 0x05
	socksNoAuth       = 0x00
	socksUserPass     = 0x02
	socksNoAcceptable = 0xFF
	socksCmdConnect   = 0x01
	socksAtypIPv4     = 0x01
	socksAtypDomain   = 0x03
	socksAtypIPv6     = 0x04
)

// SOCKS5Upstream is a user's own SOCKS5 proxy (RFC 1928) with optional
// username/password authentication (RFC 1929). Target names are sent to the
// proxy for resolution, so socks5:// and socks5h:// behave the same.
type SOCKS5Upstream struct {
	addr     string
	username string
	password string
	label    string
}

// NewSOCKS5Upstream returns an upstream for a SOCKS5 proxy. label is shown in
// status output and must not contain the password.
func NewSOCKS5Upstream(addr, username, password, label string) *SOCKS5Upstream {
	return &SOCKS5Upstream{addr: addr, username: username, password: password, label: label}
}

// Describe returns the redacted label.
func (u *SOCKS5Upstream) Describe() string { return u.label }

// DialTarget connects to the proxy and asks it to open a TCP connection to hostPort.
func (u *SOCKS5Upstream) DialTarget(ctx context.Context, hostPort string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, &DialError{Kind: KindHandshake, Msg: "invalid target address", Err: err}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, &DialError{Kind: KindHandshake, Msg: "invalid target port"}
	}

	conn, err := dialProxyConn(ctx, u.addr, false, nil)
	if err != nil {
		return nil, &DialError{Kind: KindDial, Err: err}
	}
	conn.SetDeadline(handshakeDeadline(ctx, dialTimeout))
	if err := u.handshake(conn, host, port); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}

func (u *SOCKS5Upstream) handshake(conn net.Conn, host string, port int) error {
	wantAuth := u.username != "" || u.password != ""
	methods := []byte{socksNoAuth}
	if wantAuth {
		methods = append(methods, socksUserPass)
	}
	greeting := append([]byte{socksVersion, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		return &DialError{Kind: KindDial, Err: err}
	}

	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return handshakeError("proxy did not answer the SOCKS5 greeting", err)
	}
	if reply[0] != socksVersion {
		return &DialError{Kind: KindHandshake, Msg: "not a SOCKS5 proxy"}
	}
	switch reply[1] {
	case socksNoAuth:
	case socksUserPass:
		if !wantAuth {
			return &DialError{Kind: KindCredentials, Msg: "proxy requires a username and password"}
		}
		if err := u.authenticate(conn); err != nil {
			return err
		}
	case socksNoAcceptable:
		if wantAuth {
			return &DialError{Kind: KindHandshake, Msg: "proxy accepts neither anonymous nor username/password authentication"}
		}
		return &DialError{Kind: KindCredentials, Msg: "proxy requires a username and password"}
	default:
		return &DialError{Kind: KindHandshake, Msg: fmt.Sprintf("proxy chose unsupported authentication method %d", reply[1])}
	}

	req := []byte{socksVersion, socksCmdConnect, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, socksAtypIPv4)
			req = append(req, ip4...)
		} else {
			req = append(req, socksAtypIPv6)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return &DialError{Kind: KindHandshake, Msg: "target host name longer than 255 bytes"}
		}
		req = append(req, socksAtypDomain, byte(len(host)))
		req = append(req, host...)
	}
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		return &DialError{Kind: KindDial, Err: err}
	}

	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return handshakeError("proxy did not answer the CONNECT request", err)
	}
	if head[0] != socksVersion {
		return &DialError{Kind: KindHandshake, Msg: "not a SOCKS5 proxy"}
	}
	if head[1] != 0x00 {
		msg := socksReplyText(head[1])
		if (head[1] == 0x01 || head[1] == 0x02) && wantAuth {
			// Some servers accept the credentials exchange and refuse at the
			// request instead when the password is wrong: 3proxy answers
			// "not allowed by ruleset", others "general failure".
			msg += " (check the username and password)"
		}
		return &DialError{Kind: KindProxyStatus, Status: int(head[1]), Msg: msg}
	}

	// Skip the bound address and port.
	var skip int
	switch head[3] {
	case socksAtypIPv4:
		skip = net.IPv4len
	case socksAtypIPv6:
		skip = net.IPv6len
	case socksAtypDomain:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			return handshakeError("proxy sent a truncated CONNECT reply", err)
		}
		skip = int(l[0])
	default:
		return &DialError{Kind: KindHandshake, Msg: "proxy sent an unknown address type"}
	}
	if _, err := io.CopyN(io.Discard, conn, int64(skip+2)); err != nil {
		return handshakeError("proxy sent a truncated CONNECT reply", err)
	}
	return nil
}

func (u *SOCKS5Upstream) authenticate(conn net.Conn) error {
	if len(u.username) > 255 || len(u.password) > 255 {
		return &DialError{Kind: KindCredentials, Msg: "username or password longer than 255 bytes"}
	}
	msg := []byte{0x01, byte(len(u.username))}
	msg = append(msg, u.username...)
	msg = append(msg, byte(len(u.password)))
	msg = append(msg, u.password...)
	if _, err := conn.Write(msg); err != nil {
		return &DialError{Kind: KindDial, Err: err}
	}
	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return handshakeError("proxy did not answer the authentication request", err)
	}
	if reply[1] != 0x00 {
		return &DialError{Kind: KindCredentials, Status: int(reply[1])}
	}
	return nil
}

func socksReplyText(code byte) string {
	switch code {
	case 1:
		return "general failure"
	case 2:
		return "connection not allowed by ruleset"
	case 3:
		return "network unreachable"
	case 4:
		return "host unreachable"
	case 5:
		return "connection refused"
	case 6:
		return "TTL expired"
	case 7:
		return "command not supported"
	case 8:
		return "address type not supported"
	default:
		return fmt.Sprintf("SOCKS reply %d", code)
	}
}

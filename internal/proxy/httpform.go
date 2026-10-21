package proxy

import (
	"bytes"
	"errors"
	"net"
	"net/url"
	"strings"
)

// originForm rewrites the header block of a proxy-style (absolute-form) HTTP
// request into the origin form a target server expects and returns the
// target host:port. Hop-by-hop proxy headers are dropped. Only the bytes in
// data are rewritten; the caller relays the rest of the request unchanged.
func originForm(data []byte) (hostPort string, out []byte, err error) {
	headEnd := bytes.Index(data, []byte("\r\n\r\n"))
	if headEnd == -1 {
		return "", nil, errors.New("request headers incomplete")
	}
	lines := strings.Split(string(data[:headEnd]), "\r\n")
	body := data[headEnd+4:]

	parts := strings.SplitN(lines[0], " ", 3)
	if len(parts) != 3 {
		return "", nil, errors.New("malformed request line")
	}
	method, target, version := parts[0], parts[1], parts[2]

	var hostHeader string
	for _, l := range lines[1:] {
		if len(l) > 5 && strings.EqualFold(l[:5], "host:") {
			hostHeader = strings.TrimSpace(l[5:])
		}
	}

	path := target
	host := hostHeader
	defaultPort := "80"
	if strings.Contains(target, "://") {
		u, err := url.Parse(target)
		if err != nil || u.Host == "" {
			return "", nil, errors.New("malformed request target")
		}
		host = u.Host
		path = u.RequestURI()
		if strings.EqualFold(u.Scheme, "https") {
			defaultPort = "443"
		}
	}
	if host == "" {
		return "", nil, errors.New("request has no host")
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(strings.Trim(host, "[]"), defaultPort)
	}

	var b strings.Builder
	b.WriteString(method + " " + path + " " + version + "\r\n")
	if hostHeader == "" {
		b.WriteString("Host: " + host + "\r\n")
	}
	for _, l := range lines[1:] {
		lower := strings.ToLower(l)
		if strings.HasPrefix(lower, "proxy-connection:") || strings.HasPrefix(lower, "proxy-authorization:") {
			continue
		}
		b.WriteString(l + "\r\n")
	}
	b.WriteString("\r\n")
	out = append([]byte(b.String()), body...)
	return host, out, nil
}

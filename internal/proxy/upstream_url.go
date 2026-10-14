package proxy

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ParseUpstreamURL builds an Upstream from a proxy URL such as
// http://user:pass@host:8080, https://host:443 or socks5://user:pass@host:1080.
// The port is required. Credentials go in the userinfo part, URL-encoded.
// Returned errors never contain the password.
func ParseUpstreamURL(raw string) (Upstream, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("proxy URL is empty")
	}
	if !strings.Contains(raw, "://") {
		return nil, errors.New("proxy URL needs a scheme: http, https, socks5 or socks5h")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("proxy URL is not valid; URL-encode special characters in the username and password")
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q: use http, https, socks5 or socks5h", u.Scheme)
	}
	host := u.Hostname()
	port := u.Port()
	if host == "" {
		return nil, errors.New("proxy URL needs a host")
	}
	if port == "" {
		return nil, errors.New("proxy URL needs a port, for example http://proxy.example.com:8080")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("proxy URL must not have a path, query or fragment")
	}

	user := u.User.Username()
	pass, _ := u.User.Password()
	addr := net.JoinHostPort(host, port)
	label := RedactURL(raw)

	switch scheme {
	case "http":
		return NewHTTPUpstream(addr, false, user, pass, label), nil
	case "https":
		return NewHTTPUpstream(addr, true, user, pass, label), nil
	default:
		return NewSOCKS5Upstream(addr, user, pass, label), nil
	}
}

// RedactURL returns scheme://user:***@host:port for a proxy URL, hiding the
// password. A URL that does not parse is replaced entirely so nothing leaks.
func RedactURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "(invalid proxy URL)"
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(u.Scheme))
	b.WriteString("://")
	if u.User != nil {
		b.WriteString(url.User(u.User.Username()).String())
		if _, has := u.User.Password(); has {
			b.WriteString(":***")
		}
		b.WriteString("@")
	}
	b.WriteString(u.Host)
	return b.String()
}

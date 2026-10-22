package proxy

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Hints shown with a failure, so nobody has to know the URL rules upfront.
// They never contain any part of the URL.
const (
	// QuoteHint explains the one thing the CLI cannot fix itself: the shell
	// splitting or expanding an unquoted URL before the CLI sees it.
	QuoteHint = "If the password has spaces or symbols such as $ or !, wrap the URL in single quotes"
	// encodedHint is attached when the proxy rejects credentials that contain
	// a %XX sequence: they may have been percent-encoded for an older version.
	encodedHint = "If you percent-encoded the password, write the real characters instead"
	// noCredentialsHint is attached when a proxy asked for credentials the URL
	// did not carry.
	noCredentialsHint = "Add them to the URL: scheme://user:password@host:port"
	// socksCredentialLength is RFC 1929's limit, checked before anything is sent.
	socksCredentialLength = "SOCKS5 username and password must be at most 255 bytes"
)

// ErrURLFormat is returned when the host part of a proxy URL does not parse.
// Credentials are taken as written, so a bad host part is the only way to get
// here, typically after the shell cut an unquoted URL.
var ErrURLFormat = errors.New("proxy URL is not valid: expected scheme://user:password@host:port. " + QuoteHint)

// schemeRe matches a URL scheme (RFC 3986). Anything else before "://" is
// never echoed back: it could be a misplaced password.
var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*$`)

// percentRe matches one percent-encoded byte.
var percentRe = regexp.MustCompile(`%[0-9A-Fa-f]{2}`)

// ParseUpstreamURL builds an Upstream from scheme://[user[:password]@]host:port
// as typed now. The username and password are taken byte for byte, whatever
// they contain; the text before the last "@" is the credentials, split once
// at the first ":". The port is required. Returned errors never contain the
// password.
func ParseUpstreamURL(raw string) (Upstream, error) {
	return parseUpstreamURL(raw, false)
}

// ParseSavedUpstreamURL builds an Upstream from a saved entry. Entries saved
// before 0.2.1 (literal false) hold percent-encoded credentials and are
// decoded; entries saved since (literal true) are taken as written.
func ParseSavedUpstreamURL(raw string, literal bool) (Upstream, error) {
	return parseUpstreamURL(raw, !literal)
}

func parseUpstreamURL(raw string, encoded bool) (Upstream, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("proxy URL is empty")
	}
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return nil, errors.New("proxy URL needs a scheme: http, https, socks5 or socks5h")
	}
	scheme = strings.ToLower(scheme)
	switch scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		if schemeRe.MatchString(scheme) {
			return nil, fmt.Errorf("unsupported proxy scheme %q: use http, https, socks5 or socks5h", scheme)
		}
		return nil, errors.New("unsupported proxy scheme: use http, https, socks5 or socks5h")
	}

	creds, hostport, hasCreds := cutLast(rest, "@")
	var user, pass string
	if hasCreds {
		user, pass, _ = strings.Cut(creds, ":")
		if encoded {
			user, pass = legacyDecode(user), legacyDecode(pass)
		}
	}

	host, port, err := parseHostPort(scheme, hostport)
	if err != nil {
		return nil, err
	}

	socks := scheme == "socks5" || scheme == "socks5h"
	if socks && (len(user) > 255 || len(pass) > 255) {
		return nil, errors.New(socksCredentialLength)
	}

	addr := net.JoinHostPort(host, port)
	label := RedactURL(raw)
	hint := credentialHint(hasCreds, !encoded && percentRe.MatchString(creds))
	if socks {
		up := NewSOCKS5Upstream(addr, user, pass, label)
		up.credHint = hint
		return up, nil
	}
	up := NewHTTPUpstream(addr, scheme == "https", user, pass, label)
	up.credHint = hint
	return up, nil
}

// parseHostPort validates the part after the credentials: a host and a port,
// nothing else.
func parseHostPort(scheme, hostport string) (host, port string, err error) {
	u, err := url.Parse(scheme + "://" + hostport)
	if err != nil {
		return "", "", ErrURLFormat
	}
	host, port = u.Hostname(), u.Port()
	if host == "" {
		return "", "", errors.New("proxy URL needs a host")
	}
	if port == "" {
		return "", "", errors.New("proxy URL needs a port, for example http://proxy.example.com:8080")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("proxy URL must not have a path, query or fragment")
	}
	return host, port, nil
}

// credentialHint is attached to a credentials rejection from this upstream.
func credentialHint(hasCreds, looksEncoded bool) string {
	if !hasCreds {
		return noCredentialsHint
	}
	if looksEncoded {
		return QuoteHint + ". " + encodedHint
	}
	return QuoteHint
}

// legacyDecode undoes the percent-encoding that versions before 0.2.1 asked
// for. A value that does not decode is kept as written.
func legacyDecode(v string) string {
	if d, err := url.PathUnescape(v); err == nil {
		return d
	}
	return v
}

// cutLast splits s at the last occurrence of sep.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return "", s, false
	}
	return s[:i], s[i+len(sep):], true
}

// RedactURL returns scheme://user:***@host:port for a proxy URL, hiding the
// password. It splits the text the same way as ParseUpstreamURL, so no byte
// of the password can leak whatever the password contains. Text that is not
// shaped like a proxy URL, including one cut short before its host part, is
// replaced entirely.
func RedactURL(raw string) string {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(raw), "://")
	if !ok || !schemeRe.MatchString(scheme) {
		return "(invalid proxy URL)"
	}
	creds, hostport, hasCreds := cutLast(rest, "@")
	if _, _, err := parseHostPort(strings.ToLower(scheme), hostport); err != nil {
		return "(invalid proxy URL)"
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(scheme))
	b.WriteString("://")
	if hasCreds {
		user, _, hasPass := strings.Cut(creds, ":")
		b.WriteString(user)
		if hasPass {
			b.WriteString(":***")
		}
		b.WriteString("@")
	}
	b.WriteString(hostport)
	return b.String()
}

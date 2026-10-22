package proxy

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/shellroute/shellroute-cli/internal/proxy/proxytest"
)

// literalPasswords must reach the proxy byte for byte.
var literalPasswords = []string{
	"p@ss", "p:ss", "p/ss", "p#ss", "p?ss", "pa ss", `p"ss`, "pässwörd",
	"100%", "%40", "%zz", "p@ss/w#rd?100% x",
}

// credentials returns the username and password an upstream will send.
func credentials(t *testing.T, up Upstream) (string, string) {
	t.Helper()
	switch u := up.(type) {
	case *HTTPUpstream:
		if u.auth == "" {
			return "", ""
		}
		raw, err := base64.StdEncoding.DecodeString(u.auth)
		if err != nil {
			t.Fatalf("auth is not base64: %v", err)
		}
		user, pass, _ := strings.Cut(string(raw), ":")
		return user, pass
	case *SOCKS5Upstream:
		return u.username, u.password
	}
	t.Fatalf("unexpected upstream %T", up)
	return "", ""
}

func upstreamAddr(up Upstream) string {
	switch u := up.(type) {
	case *HTTPUpstream:
		return u.addr
	case *SOCKS5Upstream:
		return u.addr
	}
	return ""
}

func hintOf(up Upstream) string {
	switch u := up.(type) {
	case *HTTPUpstream:
		return u.credHint
	case *SOCKS5Upstream:
		return u.credHint
	}
	return ""
}

func TestParseUpstreamURL_CredentialsAreLiteral(t *testing.T) {
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		for _, pass := range literalPasswords {
			raw := scheme + "://user:" + pass + "@proxy.example:1080"
			t.Run(raw, func(t *testing.T) {
				up, err := ParseUpstreamURL(raw)
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				user, got := credentials(t, up)
				if user != "user" || got != pass {
					t.Errorf("credentials = %q / %q, want user / %q", user, got, pass)
				}
				if addr := upstreamAddr(up); addr != "proxy.example:1080" {
					t.Errorf("addr = %q", addr)
				}
				want := scheme + "://user:***@proxy.example:1080"
				if d := up.Describe(); d != want || strings.Contains(d, pass) {
					t.Errorf("Describe = %q, want %q", d, want)
				}
				if r := RedactURL(raw); r != want || strings.Contains(r, pass) {
					t.Errorf("RedactURL = %q, want %q", r, want)
				}
			})
		}
	}
}

func TestParseUpstreamURL_Shapes(t *testing.T) {
	cases := []struct{ raw, user, pass, addr, label string }{
		{"http://user@h:1", "user", "", "h:1", "http://user@h:1"},
		{"http://user:@h:1", "user", "", "h:1", "http://user:***@h:1"},
		{"http://h:1", "", "", "h:1", "http://h:1"},
		{"http://u:p@[2001:db8::1]:8080", "u", "p", "[2001:db8::1]:8080", "http://u:***@[2001:db8::1]:8080"},
		{"http://a@b:c@d@h:1", "a@b", "c@d", "h:1", "http://a@b:***@h:1"},
		{"  HTTP://u:p@h:1  ", "u", "p", "h:1", "http://u:***@h:1"},
		{"http://u:" + strings.Repeat("x", 300) + "@h:1", "u", strings.Repeat("x", 300), "h:1", "http://u:***@h:1"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			up, err := ParseUpstreamURL(tc.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			user, pass := credentials(t, up)
			if user != tc.user || pass != tc.pass {
				t.Errorf("credentials = %q / %q, want %q / %q", user, pass, tc.user, tc.pass)
			}
			if got := upstreamAddr(up); got != tc.addr {
				t.Errorf("addr = %q, want %q", got, tc.addr)
			}
			if got := up.Describe(); got != tc.label {
				t.Errorf("Describe = %q, want %q", got, tc.label)
			}
		})
	}
}

func TestParseUpstreamURL_Errors(t *testing.T) {
	cases := map[string]string{
		"":                           "empty",
		"proxy.example:8080":         "needs a scheme",
		"ftp://u:topsecret@h:21":     `unsupported proxy scheme "ftp"`,
		"u:topsecret@http://h:1":     "unsupported proxy scheme: use",
		"http://u:topsecret@h":       "needs a port",
		"http://u:topsecret@:8080":   "needs a host",
		"http://u:topsecret@":        "needs a host",
		"http://u:topsecret@h:1/x":   "must not have a path",
		"http://u:topsecret@h:1?x=1": "must not have a path",
		"http://u:topsecret@h:1#x":   "must not have a path",
		"http://u:topsecret@h:1 x":   "single quotes",
		"http://u:topsecret":         "single quotes",
		"socks5://u:" + strings.Repeat("x", 256) + "@h:1": "255 bytes",
	}
	for raw, want := range cases {
		_, err := ParseUpstreamURL(raw)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want containing %q", raw, err, want)
			continue
		}
		if strings.Contains(err.Error(), "topsecret") || strings.Contains(err.Error(), "xxxx") {
			t.Errorf("%q: error echoes the credentials: %v", raw, err)
		}
	}
}

func TestRedactURL_NeverLeaksAnyPasswordShape(t *testing.T) {
	for _, pass := range append(literalPasswords, "top/secret@evil", "s://ecret") {
		for _, raw := range []string{
			"http://user:" + pass + "@h:1",
			"socks5://user:" + pass + "@[::1]:1080",
			"http://user:" + pass, // host part missing
			"user:" + pass + "@http://h:1",
		} {
			if got := RedactURL(raw); strings.Contains(got, pass) {
				t.Errorf("RedactURL(%q) = %q leaks the password", raw, got)
			}
		}
	}
}

func TestParseSavedUpstreamURL_LegacyEntriesAreDecoded(t *testing.T) {
	raw := "http://al%40ice:p%3Ass@proxy.example:8080"
	up, err := ParseSavedUpstreamURL(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	if user, pass := credentials(t, up); user != "al@ice" || pass != "p:ss" {
		t.Errorf("legacy entry decoded to %q / %q", user, pass)
	}
	up, err = ParseSavedUpstreamURL(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if user, pass := credentials(t, up); user != "al%40ice" || pass != "p%3Ass" {
		t.Errorf("literal entry changed to %q / %q", user, pass)
	}
	up, err = ParseSavedUpstreamURL("http://u:100%@h:1", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, pass := credentials(t, up); pass != "100%" {
		t.Errorf("undecodable legacy password became %q", pass)
	}
}

func TestParseUpstreamURL_CredentialHints(t *testing.T) {
	cases := map[string]string{
		"http://u:p@h:1":    QuoteHint,
		"http://u:p%41@h:1": encodedHint,
		"http://h:1":        noCredentialsHint,
	}
	for raw, want := range cases {
		up, err := ParseUpstreamURL(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(hintOf(up), want) {
			t.Errorf("%q: hint = %q, want containing %q", raw, hintOf(up), want)
		}
	}
	up, _ := ParseSavedUpstreamURL("http://u:p%41@h:1", false)
	if strings.Contains(hintOf(up), encodedHint) {
		t.Error("a legacy entry was percent-encoded on purpose; no encoding hint")
	}
}

func TestExplain_CredentialsRejectedCarriesHint(t *testing.T) {
	p := proxytest.StartHTTPProxy(t, "alice", "right")
	up, err := ParseUpstreamURL("http://alice:wr%41ng@" + p.Addr)
	if err != nil {
		t.Fatal(err)
	}
	_, err = up.DialTarget(context.Background(), "203.0.113.1:443")
	if err == nil {
		t.Fatal("expected a credentials rejection")
	}
	got := Explain(err)
	for _, want := range []string{"proxy rejected credentials", QuoteHint, encodedHint} {
		if !strings.Contains(got, want) {
			t.Errorf("Explain = %q, want containing %q", got, want)
		}
	}
	if strings.Contains(got, "wr%41ng") || strings.Contains(got, "wrAng") {
		t.Errorf("Explain leaks the password: %q", got)
	}

	s := proxytest.StartSOCKS5(t, "alice", "right")
	up, _ = ParseUpstreamURL("socks5://" + s.Addr)
	_, err = up.DialTarget(context.Background(), "203.0.113.1:443")
	if err == nil || !strings.Contains(Explain(err), noCredentialsHint) {
		t.Errorf("SOCKS5 without credentials: Explain = %q", Explain(err))
	}
}

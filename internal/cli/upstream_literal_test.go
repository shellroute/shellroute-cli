package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/proxy/proxytest"
)

// literalPassword has every character the old rules made people encode.
const literalPassword = "p@ss/w#rd?100% x"

const exitIPBody = `{"ip": "203.0.113.5", "country": "LT"}`

// captureStderr returns what f wrote to stderr.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	f()
	w.Close()
	os.Stderr = old
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestUpstreamTest_LiteralPasswordHTTP(t *testing.T) {
	withOwnConfig(t)
	p := proxytest.StartHTTPProxy(t, "alice", literalPassword)
	withExitIPEndpoints(t, ipEcho(t, exitIPBody).URL)
	if _, err := execCLI(t, "upstream", "add", "work", "http://alice:"+literalPassword+"@"+p.Addr); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := execCLI(t, "upstream", "test", "work"); err != nil {
		t.Fatalf("test: %v", err)
	}
	if p.Requests.Load() == 0 || p.Rejected.Load() != 0 {
		t.Errorf("proxy saw %d authenticated and %d rejected requests", p.Requests.Load(), p.Rejected.Load())
	}
}

func TestUpstreamTest_LiteralPasswordSOCKS5ByURL(t *testing.T) {
	withOwnConfig(t)
	s := proxytest.StartSOCKS5(t, "alice", literalPassword)
	withExitIPEndpoints(t, ipEcho(t, exitIPBody).URL)
	if _, err := execCLI(t, "upstream", "test", "socks5://alice:"+literalPassword+"@"+s.Addr); err != nil {
		t.Fatalf("test: %v", err)
	}
	if s.Connects.Load() == 0 || s.Rejected.Load() != 0 {
		t.Errorf("proxy saw %d connects and %d rejected logins", s.Connects.Load(), s.Rejected.Load())
	}
}

func TestUpstreamTest_LegacyEncodedEntryStillWorks(t *testing.T) {
	withOwnConfig(t)
	p := proxytest.StartHTTPProxy(t, "alice", "p@ss")
	withExitIPEndpoints(t, ipEcho(t, exitIPBody).URL)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	// Written by 0.2.0: percent-encoded, no literal marker.
	cfg.AddUpstream(config.Upstream{Name: "old", URL: "http://alice:p%40ss@" + p.Addr})
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := execCLI(t, "upstream", "test", "old"); err != nil {
		t.Fatalf("legacy entry: %v", err)
	}
	if p.Requests.Load() == 0 {
		t.Error("the decoded password should have authenticated")
	}
}

func TestUpstreamAdd_SavesLiteralMarker(t *testing.T) {
	withOwnConfig(t)
	p := proxytest.StartHTTPProxy(t, "alice", "p%40ss")
	withExitIPEndpoints(t, ipEcho(t, exitIPBody).URL)
	if _, err := execCLI(t, "upstream", "add", "new", "http://alice:p%40ss@"+p.Addr); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := cfg.FindUpstream("new")
	if !ok || !entry.Literal {
		t.Fatalf("saved entry = %+v, want Literal true", entry)
	}
	if _, err := execCLI(t, "upstream", "test", "new"); err != nil {
		t.Fatalf("literal %%40 password: %v", err)
	}
	if p.Requests.Load() == 0 {
		t.Error("the literal password should have authenticated")
	}
}

func TestUpstreamAdd_TooManyArgsHintsQuoting(t *testing.T) {
	withOwnConfig(t)
	_, err := execCLI(t, "upstream", "add", "work", "http://u:pa", "ss@h:1")
	if err == nil || !strings.Contains(err.Error(), "single quotes") {
		t.Errorf("err = %v, want the quoting hint", err)
	}
	_, err = execCLI(t, "upstream", "test", "http://u:pa", "ss@h:1")
	if err == nil || !strings.Contains(err.Error(), "single quotes") {
		t.Errorf("test: err = %v, want the quoting hint", err)
	}
}

func TestRun_ProxyFlagSplitByShellHintsQuoting(t *testing.T) {
	withOwnConfig(t)
	_, err := execCLI(t, "run", "--proxy", "http://u:pa", "ss@h:1", "--", "echo")
	if err == nil || !strings.Contains(err.Error(), "single quotes") {
		t.Errorf("err = %v, want the quoting hint", err)
	}
}

func TestUpstreamTest_RejectedCredentialsShowHints(t *testing.T) {
	withOwnConfig(t)
	p := proxytest.StartHTTPProxy(t, "alice", "right")
	withExitIPEndpoints(t, "http://127.0.0.1:1/ip")
	if _, err := execCLI(t, "upstream", "add", "work", "http://alice:wr%41ng@"+p.Addr); err != nil {
		t.Fatal(err)
	}
	out := captureStderr(t, func() {
		if _, err := execCLI(t, "upstream", "test", "work"); err == nil {
			t.Error("expected a failure")
		}
	})
	for _, want := range []string{"rejected credentials", "single quotes", "percent-encoded"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "wr%41ng") {
		t.Errorf("stderr shows the password:\n%s", out)
	}
}

func TestRunOwn_LiteralPasswordEndToEnd(t *testing.T) {
	for _, scheme := range []string{"http", "socks5"} {
		t.Run(scheme, func(t *testing.T) {
			var addr string
			var authenticated func() int32
			if scheme == "http" {
				p := proxytest.StartHTTPProxy(t, "alice", literalPassword)
				addr, authenticated = p.Addr, p.Requests.Load
			} else {
				s := proxytest.StartSOCKS5(t, "alice", literalPassword)
				addr, authenticated = s.Addr, s.Connects.Load
			}
			echo := ipEcho(t, exitIPBody)
			res := runHelper(t, false,
				"SR_TEST_HELPER=run-own-url",
				"SHELLROUTE_HOME="+t.TempDir(),
				"SHELLROUTE_API_KEY=",
				"SHELLROUTE_PROXY=",
				"SHELLROUTE_API_URL=http://127.0.0.1:1",
				"SR_TEST_PROXY_URL="+scheme+"://alice:"+literalPassword+"@"+addr,
				"SR_TEST_EXIT_IP_URL="+echo.URL,
			)
			if res.sig != 0 || res.code != 0 {
				t.Fatalf("exit %d, signal %v:\n%s", res.code, res.sig, res.out)
			}
			if authenticated() == 0 {
				t.Error("the proxy never saw an authenticated request")
			}
			if strings.Contains(res.out, literalPassword) {
				t.Errorf("output shows the password:\n%s", res.out)
			}
		})
	}
}

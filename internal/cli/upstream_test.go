package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shellroute/shellroute-cli/internal/proxy"
	"github.com/shellroute/shellroute-cli/internal/proxy/proxytest"
)

// withOwnConfig points the CLI at an empty temporary config with no account.
func withOwnConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SHELLROUTE_HOME", dir)
	t.Setenv("SHELLROUTE_API_KEY", "")
	t.Setenv(envProxyVar, "")
	return dir
}

// execCLI runs the root command with args and returns what it wrote to stdout.
func execCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetAllFlags()
	for _, c := range upstreamCmd.Commands() {
		resetFlags(c)
	}
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	defer rootCmd.SetOut(nil)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}

func withExitIPEndpoints(t *testing.T, endpoints ...string) {
	t.Helper()
	old := proxy.ExitInfoEndpoints
	proxy.ExitInfoEndpoints = endpoints
	t.Cleanup(func() { proxy.ExitInfoEndpoints = old })
}

func ipEcho(t *testing.T, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestUpstreamSubcommandsRegistered(t *testing.T) {
	got := map[string]bool{}
	for _, c := range upstreamCmd.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"add", "list", "remove", "test"} {
		if !got[want] {
			t.Errorf("upstream %s not registered", want)
		}
	}
}

func TestUpstreamAdd_RejectsBadNames(t *testing.T) {
	withOwnConfig(t)
	cases := map[string]string{
		"de":        "country code",
		"US":        "country code",
		"Work":      "3 to 32",
		"a":         "3 to 32",
		"my proxy":  "3 to 32",
		"proxy.one": "3 to 32",
	}
	for name, want := range cases {
		_, err := execCLI(t, "upstream", "add", name, "http://proxy.example:8080")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("add %q: err = %v, want containing %q", name, err, want)
		}
	}
}

func TestUpstreamAdd_RejectsBadURLs(t *testing.T) {
	withOwnConfig(t)
	cases := map[string]string{
		"ftp://proxy.example:21":      "unsupported proxy scheme",
		"http://proxy.example":        "needs a port",
		"proxy.example:8080":          "needs a scheme",
		"http://proxy.example:1/x":    "must not have a path",
		"socks4://proxy.example:1080": "unsupported proxy scheme",
	}
	for raw, want := range cases {
		_, err := execCLI(t, "upstream", "add", "work", raw)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("add %q: err = %v, want containing %q", raw, err, want)
		}
	}
}

func TestUpstreamAddListRemove(t *testing.T) {
	dir := withOwnConfig(t)

	if _, err := execCLI(t, "upstream", "add", "work", "http://alice:s3cret@proxy.example:8080", "--note", "office"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := execCLI(t, "upstream", "add", "home", "socks5://home.example:1080"); err != nil {
		t.Fatalf("add home: %v", err)
	}

	out, err := execCLI(t, "upstream", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"NAME", "work", "http://alice:***@proxy.example:8080", "office", "home", "socks5://home.example:1080"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s3cret") {
		t.Errorf("list leaks the password:\n%s", out)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "s3cret") {
		t.Error("config file should store the full URL")
	}
	info, _ := os.Stat(filepath.Join(dir, "config.toml"))
	if info.Mode().Perm() != 0600 {
		t.Errorf("config mode = %o, want 0600", info.Mode().Perm())
	}

	// Same name again updates in place.
	if _, err := execCLI(t, "upstream", "add", "work", "http://alice:new@proxy.example:8080"); err != nil {
		t.Fatalf("update: %v", err)
	}
	out, _ = execCLI(t, "upstream", "list")
	if strings.Count(out, "work") != 1 || strings.Contains(out, "office") {
		t.Errorf("update should replace the entry:\n%s", out)
	}

	if _, err := execCLI(t, "upstream", "remove", "work"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := execCLI(t, "upstream", "remove", "work"); err == nil || !strings.Contains(err.Error(), "unknown upstream") {
		t.Errorf("second remove: err = %v", err)
	}
	out, _ = execCLI(t, "upstream", "list")
	if strings.Contains(out, "work") || !strings.Contains(out, "home") {
		t.Errorf("after remove:\n%s", out)
	}
}

func TestUpstreamList_EmptyWritesNothingToStdout(t *testing.T) {
	withOwnConfig(t)
	out, err := execCLI(t, "upstream", "list")
	if err != nil || out != "" {
		t.Errorf("out=%q err=%v", out, err)
	}
}

func TestUpstreamTest_UnknownName(t *testing.T) {
	withOwnConfig(t)
	_, err := execCLI(t, "upstream", "test", "nope")
	if err == nil || !strings.Contains(err.Error(), "unknown upstream") {
		t.Errorf("err = %v", err)
	}
}

func TestUpstreamTest_CredentialsError(t *testing.T) {
	withOwnConfig(t)
	p := proxytest.StartHTTPProxy(t, "alice", "right")
	withExitIPEndpoints(t, "http://127.0.0.1:1/ip")
	if _, err := execCLI(t, "upstream", "add", "work", "http://alice:wrong@"+p.Addr); err != nil {
		t.Fatal(err)
	}
	_, err := execCLI(t, "upstream", "test", "work")
	if err == nil || err.Error() != "upstream unreachable" {
		t.Errorf("err = %v, want upstream unreachable", err)
	}
	if p.Rejected.Load() == 0 {
		t.Error("the proxy should have seen the bad credentials")
	}
}

func TestUpstreamTest_URLSuccess(t *testing.T) {
	withOwnConfig(t)
	p := proxytest.StartHTTPProxy(t, "", "")
	withExitIPEndpoints(t, ipEcho(t, `{"ip": "203.0.113.5", "country": "LT"}`).URL)
	if _, err := execCLI(t, "upstream", "test", "http://"+p.Addr); err != nil {
		t.Errorf("test by URL: %v", err)
	}
	if p.Requests.Load() == 0 {
		t.Error("the probe should have gone through the proxy")
	}
}

func TestUpstreamTest_ExitIPUnknownIsNotAnError(t *testing.T) {
	withOwnConfig(t)
	p := proxytest.StartSOCKS5(t, "", "")
	withExitIPEndpoints(t, ipEcho(t, "<html>").URL)
	if _, err := execCLI(t, "upstream", "test", "socks5://"+p.Addr); err != nil {
		t.Errorf("tunnel open but no IP should warn, not fail: %v", err)
	}
}

func TestUpstreamCommands_WorkWithAPIUnreachable(t *testing.T) {
	withOwnConfig(t)
	t.Setenv("SHELLROUTE_API_URL", "http://127.0.0.1:1")
	if !ownRouteSelected(upstreamListCmd, nil) {
		t.Error("upstream commands must skip the version check")
	}
	if _, err := execCLI(t, "upstream", "add", "work", "http://proxy.example:8080"); err != nil {
		t.Errorf("add with API down: %v", err)
	}
	if _, err := execCLI(t, "upstream", "list"); err != nil {
		t.Errorf("list with API down: %v", err)
	}
}

package session

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/shellroute/shellroute-cli/internal/proxy"
)

// directUpstream dials the target itself; it stands in for a working proxy.
type directUpstream struct{ calls atomic.Int32 }

func (d *directUpstream) DialTarget(ctx context.Context, hostPort string) (net.Conn, error) {
	d.calls.Add(1)
	return (&net.Dialer{}).DialContext(ctx, "tcp", hostPort)
}
func (d *directUpstream) Describe() string { return "http://user:***@proxy.test:8080" }

// failingUpstream never opens a tunnel.
type failingUpstream struct{ kind proxy.DialErrorKind }

func (f *failingUpstream) DialTarget(ctx context.Context, hostPort string) (net.Conn, error) {
	return nil, &proxy.DialError{Kind: f.kind, Err: errors.New("down")}
}
func (f *failingUpstream) Describe() string { return "failing" }

func withExitIPEndpoints(t *testing.T, endpoints ...string) {
	t.Helper()
	old := proxy.ExitIPEndpoints
	proxy.ExitIPEndpoints = endpoints
	t.Cleanup(func() { proxy.ExitIPEndpoints = old })
}

func echoIP(t *testing.T, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestStartOwn_NoAPIAndStop(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHELLROUTE_HOME", dir)
	withExitIPEndpoints(t, echoIP(t, "203.0.113.9\n").URL)

	up := &directUpstream{}
	sess, err := StartOwn(context.Background(), up, "work", "DE", 0, StartOpts{Mode: "run", TrackRelays: true})
	if err != nil {
		t.Fatalf("StartOwn: %v", err)
	}
	if !sess.Own || sess.UpstreamName != "work" || sess.Country != "DE" {
		t.Errorf("session = %+v", sess)
	}
	if sess.GetExitIP() != "203.0.113.9" {
		t.Errorf("exit IP = %q", sess.GetExitIP())
	}
	if sess.Via != up.Describe() {
		t.Errorf("Via = %q", sess.Via)
	}
	if sess.client != nil {
		t.Error("own session must not hold an API client")
	}

	data, err := os.ReadFile(filepath.Join(dir, sess.sessionFileName()))
	if err != nil {
		t.Fatalf("session file: %v", err)
	}
	for _, want := range []string{`"upstream": "work"`, `"via": "http://user:***@proxy.test:8080"`, `"country": "DE"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("session file lacks %s:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "pass") {
		t.Error("session file must never carry the URL password")
	}

	resp, err := sess.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if resp.CostUSD != 0 || resp.BalanceUSD != 0 {
		t.Error("own session has no cost or balance")
	}
	if _, err := os.Stat(filepath.Join(dir, sess.sessionFileName())); !os.IsNotExist(err) {
		t.Error("session file should be removed on Stop")
	}
}

func TestStartOwn_TunnelFailureReturnsDialError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHELLROUTE_HOME", dir)
	withExitIPEndpoints(t, "http://127.0.0.1:1/ip")

	_, err := StartOwn(context.Background(), &failingUpstream{kind: proxy.KindDial}, "work", "", 0, StartOpts{})
	if !proxy.TunnelFailed(err) {
		t.Fatalf("err = %v, want tunnel failure", err)
	}
	entries, _ := filepath.Glob(filepath.Join(dir, "session-*.json"))
	if len(entries) != 0 {
		t.Errorf("session file left behind: %v", entries)
	}
}

func TestStartOwn_ProbeFailureStillStartsWithWarning(t *testing.T) {
	t.Setenv("SHELLROUTE_HOME", t.TempDir())
	withExitIPEndpoints(t, echoIP(t, "<html>not an ip</html>").URL)

	sess, err := StartOwn(context.Background(), &directUpstream{}, "", "", 0, StartOpts{})
	if err != nil {
		t.Fatalf("StartOwn should succeed when only the echo service misbehaves: %v", err)
	}
	defer sess.Stop()
	if sess.ProbeWarning == "" {
		t.Error("ProbeWarning should explain the missing exit IP")
	}
	if sess.GetExitIP() != "" {
		t.Error("no exit IP should be set")
	}
	if sess.ID != "own-url" {
		t.Errorf("ad-hoc URL session ID = %q", sess.ID)
	}
}

func TestStartOwn_SkipExitIPMakesNoProbe(t *testing.T) {
	t.Setenv("SHELLROUTE_HOME", t.TempDir())
	withExitIPEndpoints(t, "http://127.0.0.1:1/ip")

	up := &directUpstream{}
	sess, err := StartOwn(context.Background(), up, "work", "DE", 0, StartOpts{SkipExitIP: true})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()
	if up.calls.Load() != 0 {
		t.Errorf("DialTarget called %d times, want 0", up.calls.Load())
	}
}

func TestStartOwn_ProxyRoutesThroughUpstream(t *testing.T) {
	t.Setenv("SHELLROUTE_HOME", t.TempDir())
	target := echoIP(t, "198.51.100.1")
	withExitIPEndpoints(t, target.URL)

	up := &directUpstream{}
	sess, err := StartOwn(context.Background(), up, "work", "", 0, StartOpts{})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	// A request through the local proxy must go out through the upstream.
	before := up.calls.Load()
	if ip := detectExitIP(context.Background(), sess.Port); ip != "198.51.100.1" {
		t.Fatalf("exit IP via local proxy = %q", ip)
	}
	if up.calls.Load() <= before {
		t.Error("local proxy did not use the upstream")
	}
	if sess.Meter().BytesTotal() == 0 {
		t.Error("bytes were not metered")
	}
}

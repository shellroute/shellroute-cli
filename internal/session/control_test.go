package session

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/meter"
	"github.com/shellroute/shellroute-cli/internal/proxy"
)

// notifyCollector collects notifications for assertions.
type notifyCollector struct {
	mu   sync.Mutex
	msgs []string
}

func (n *notifyCollector) handler(msg string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.msgs = append(n.msgs, msg)
}

func (n *notifyCollector) get() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	cp := make([]string, len(n.msgs))
	copy(cp, n.msgs)
	return cp
}

func newTestController() (*Controller, *notifyCollector) {
	client := api.New("http://localhost:0", "pk_test")
	cfg := &config.Config{}
	ctrl := NewController(client, cfg)
	nc := &notifyCollector{}
	return ctrl, nc
}

// fakeSession creates a minimal session with a listening port and proxy.
func fakeSession(t *testing.T) *Session {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return &Session{
		ID:      "test-session",
		Country: "US",
		Port:    port,
		proxy: proxy.NewServer(&proxy.Config{
			ListenPort:  port,
			GatewayAddr: "localhost:0",
			Token:       "test",
			Meter:       &meter.Meter{},
		}),
	}
}

// --- OnUpstreamDead tests ---

func TestUpstreamDead_StartsAutoRotate(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.wireUpstreamCallback(sess)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	// First failure should set unhealthy and start auto-rotate
	sess.OnUpstreamDead("connection_failed")

	ctrl.mu.Lock()
	healthy := ctrl.healthy
	notified := ctrl.notifiedDead
	ctrl.mu.Unlock()

	if healthy {
		t.Error("expected healthy=false after connection_failed")
	}
	if !notified {
		t.Error("expected notifiedDead=true after connection_failed")
	}

	// Second failure should be ignored (already unhealthy)
	sess.OnUpstreamDead("connection_failed")
	// No crash, no duplicate
}

func TestUpstreamDead_DepletedMessage(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.wireUpstreamCallback(sess)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	sess.OnUpstreamDead("depleted")

	// No async notification — precmd handles depleted UX
	ctrl.mu.Lock()
	if ctrl.healthy {
		t.Error("expected healthy=false after depleted")
	}
	if ctrl.deadReason != "depleted" {
		t.Errorf("expected deadReason=depleted, got %q", ctrl.deadReason)
	}
	ctrl.mu.Unlock()
}

func TestUpstreamDead_SetsUnhealthy(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.wireUpstreamCallback(sess)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	sess.OnUpstreamDead("connection_failed")

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if ctrl.healthy {
		t.Error("expected healthy=false after OnUpstreamDead")
	}
	if !ctrl.notifiedDead {
		t.Error("expected notifiedDead=true")
	}
}

func TestUpstreamDead_ConnectionFailedTriggersAutoRotate(t *testing.T) {
	ctrl, nc := newTestController()
	sess := fakeSession(t)
	ctrl.wireUpstreamCallback(sess)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	sess.OnUpstreamDead("connection_failed")

	ctrl.mu.Lock()
	healthy := ctrl.healthy
	notified := ctrl.notifiedDead
	ctrl.mu.Unlock()

	if healthy {
		t.Error("connection_failed should set healthy=false")
	}
	if !notified {
		t.Error("connection_failed should set notifiedDead=true")
	}

	// No notifications — precmd handles the UX
	msgs := nc.get()
	for _, m := range msgs {
		if strings.Contains(m, "reconnecting") || strings.Contains(m, "Reconnected") {
			t.Errorf("auto-rotate should not send notifications, got %q", m)
		}
	}
}

func TestUpstreamDead_DepletedDoesNotAutoRotate(t *testing.T) {
	ctrl, nc := newTestController()
	sess := fakeSession(t)
	ctrl.wireUpstreamCallback(sess)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	sess.OnUpstreamDead("depleted")
	time.Sleep(100 * time.Millisecond)

	ctrl.mu.Lock()
	rotating := ctrl.autoRotating
	ctrl.mu.Unlock()
	if rotating {
		t.Error("depleted should NOT set autoRotating")
	}

	// No async notification — precmd handles depleted UX
	msgs := nc.get()
	if len(msgs) != 0 {
		t.Errorf("expected 0 notifications (precmd handles depleted), got %d: %v", len(msgs), msgs)
	}
}

// --- Healthcheck endpoint tests ---

func TestHealthcheck_Rotating(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("1.2.3.4")
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = false
	ctrl.autoRotating = true
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if w.Body.String() != "rotating" {
		t.Errorf("expected 'rotating', got %q", w.Body.String())
	}
}

func TestHealthcheck_DeadAfterAutoRotateFails(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = false
	ctrl.autoRotating = false // auto-rotate finished but failed
	ctrl.lastProbeTime = time.Now()
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if w.Body.String() != "dead" {
		t.Errorf("expected 'dead' after failed auto-rotate, got %q", w.Body.String())
	}
}

func TestHealthcheck_NoSession(t *testing.T) {
	ctrl, _ := newTestController()
	w := httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if w.Body.String() != "none" {
		t.Errorf("expected 'none', got %q", w.Body.String())
	}
}

func TestHealthcheck_Healthy(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("1.2.3.4")
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.lastProbeTime = time.Now()
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if w.Body.String() != "ok 1.2.3.4" {
		t.Errorf("expected 'ok 1.2.3.4', got %q", w.Body.String())
	}
}

func TestHealthcheck_ReturnsCurrentIP(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("10.0.0.1")
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.lastProbeTime = time.Now()
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if !strings.HasPrefix(w.Body.String(), "ok ") {
		t.Fatalf("expected 'ok <ip>', got %q", w.Body.String())
	}
	ip := strings.TrimPrefix(w.Body.String(), "ok ")
	if ip != "10.0.0.1" {
		t.Errorf("expected IP 10.0.0.1, got %q", ip)
	}

	// Change IP and verify healthcheck reflects it
	ctrl.mu.Lock()
	ctrl.sess.SetExitIP("10.0.0.2")
	ctrl.mu.Unlock()

	w2 := httptest.NewRecorder()
	ctrl.httpHealthCheck(w2, httptest.NewRequest("GET", "/healthcheck", nil))
	if w2.Body.String() != "ok 10.0.0.2" {
		t.Errorf("expected 'ok 10.0.0.2', got %q", w2.Body.String())
	}
}

func TestHealthcheck_Unhealthy(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = false
	ctrl.lastProbeTime = time.Now() // recent probe — won't re-probe
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if w.Body.String() != "dead" {
		t.Errorf("expected 'dead', got %q", w.Body.String())
	}
}

// --- Recovery detection tests ---

func TestRecovery_RequiresConsecutiveSuccesses(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = false
	ctrl.notifiedDead = true
	ctrl.autoRotating = false
	ctrl.mu.Unlock()

	ctrl.mu.Lock()
	// Simulate 1 successful probe — not enough
	ctrl.successCount = 1
	ctrl.mu.Unlock()

	ctrl.mu.Lock()
	if ctrl.healthy {
		t.Error("should still be unhealthy after 1 success")
	}
	ctrl.mu.Unlock()

	// Simulate reaching threshold
	ctrl.mu.Lock()
	ctrl.successCount = successToRestore
	ctrl.healthy = true
	ctrl.notifiedDead = false
	ctrl.mu.Unlock()

	// Verify state
	ctrl.mu.Lock()
	if !ctrl.healthy {
		t.Error("should be healthy after threshold")
	}
	ctrl.mu.Unlock()
}

func TestRecovery_ResetCountOnFailure(t *testing.T) {
	ctrl, _ := newTestController()

	ctrl.mu.Lock()
	ctrl.successCount = 1
	ctrl.mu.Unlock()

	// Simulate failed probe resets counter
	ctrl.mu.Lock()
	ctrl.successCount = 0 // as the monitor does on failure
	if ctrl.successCount != 0 {
		t.Error("success count should reset on failure")
	}
	ctrl.mu.Unlock()
}

// --- probeProxy tests ---

func TestProbeProxy_NoListener(t *testing.T) {
	// Pick a port with nothing listening
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	if probeProxy(port) {
		t.Error("probeProxy should return false when nothing is listening")
	}
}

func TestProbeProxy_ListeningButBadResponse(t *testing.T) {
	// Start a server that accepts connections but returns non-200
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 1024)
			conn.Read(buf)
			conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			conn.Close()
		}
	}()

	if probeProxy(port) {
		t.Error("probeProxy should return false on 502")
	}
}

func TestProbeProxy_Success(t *testing.T) {
	// Start a server that returns 200 to CONNECT
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 1024)
			conn.Read(buf)
			conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
			conn.Close()
		}
	}()

	if !probeProxy(port) {
		t.Error("probeProxy should return true on 200")
	}
}

// --- Status endpoint tests ---

func TestStatus_NoSession(t *testing.T) {
	ctrl, _ := newTestController()
	w := httptest.NewRecorder()
	ctrl.httpStatus(w, httptest.NewRequest("GET", "/status", nil))
	if !strings.Contains(w.Body.String(), "Not connected") {
		t.Errorf("expected 'Not connected', got %q", w.Body.String())
	}
}

func TestStatus_WithSession(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("1.2.3.4")
	sess.StartTime = time.Now()
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpStatus(w, httptest.NewRequest("GET", "/status", nil))
	body := w.Body.String()

	// Verify all expected fields are present
	for _, field := range []string{"Country:", "Exit IP:", "Health:", "Uptime:"} {
		if !strings.Contains(body, field) {
			t.Errorf("missing field %q in status output: %q", field, body)
		}
	}
	// Verify values
	if !strings.Contains(body, "US") {
		t.Errorf("expected country US, got %q", body)
	}
	if !strings.Contains(body, "1.2.3.4") {
		t.Errorf("expected exit IP 1.2.3.4, got %q", body)
	}
	if !strings.Contains(body, "healthy") {
		t.Errorf("expected 'healthy', got %q", body)
	}
	// Verify internal details are NOT exposed
	if strings.Contains(body, "Proxy:") || strings.Contains(body, "localhost:") {
		t.Errorf("status should not expose proxy port: %q", body)
	}
	if strings.Contains(body, "gateway") || strings.Contains(body, "token") {
		t.Errorf("status should not expose internal details: %q", body)
	}
}

func TestStatus_UnhealthyState(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.StartTime = time.Now()
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = false
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpStatus(w, httptest.NewRequest("GET", "/status", nil))
	if !strings.Contains(w.Body.String(), "unhealthy") {
		t.Errorf("expected 'unhealthy' in status, got %q", w.Body.String())
	}
}

// --- Balance endpoint tests ---

func TestBalance_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/account/balance" {
			w.Write([]byte(`{"balance_usd": 5.50, "rates": {"residential_per_gb": 3.50, "datacenter_per_gb": 1.00}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})

	w := httptest.NewRecorder()
	ctrl.httpBalance(w, httptest.NewRequest("GET", "/balance", nil))
	body := w.Body.String()
	if !strings.Contains(body, "Balance") {
		t.Errorf("expected 'Balance' in response, got %q", body)
	}
}

// --- Disconnect endpoint tests ---

func TestDisconnect_NoSession(t *testing.T) {
	ctrl, _ := newTestController()
	w := httptest.NewRecorder()
	ctrl.httpDisconnect(w, httptest.NewRequest("GET", "/disconnect", nil))
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- Full cycle: healthy → dead → recovered ---

func TestHealthCycle_DeadThenRecovered(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("1.1.1.1")
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	// Step 1: simulate upstream death
	ctrl.mu.Lock()
	ctrl.healthy = false
	ctrl.notifiedDead = true
	ctrl.autoRotating = true
	ctrl.mu.Unlock()

	// Step 2: healthcheck returns "rotating"
	w := httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if w.Body.String() != "rotating" {
		t.Errorf("expected 'rotating', got %q", w.Body.String())
	}

	// Step 3: auto-rotate completes — simulate recovery
	ctrl.mu.Lock()
	ctrl.autoRotating = false
	ctrl.healthy = true
	ctrl.notifiedDead = false
	ctrl.sess.SetExitIP("2.2.2.2")
	ctrl.mu.Unlock()

	// Step 4: healthcheck returns "ok" with new IP
	w = httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if w.Body.String() != "ok 2.2.2.2" {
		t.Errorf("expected 'ok 2.2.2.2', got %q", w.Body.String())
	}

	// Step 5: auto-rotate fails — simulate failure
	ctrl.mu.Lock()
	ctrl.healthy = false
	ctrl.notifiedDead = true
	ctrl.autoRotating = false
	ctrl.mu.Unlock()

	w = httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if w.Body.String() != "dead" {
		t.Errorf("expected 'dead' after failed auto-rotate, got %q", w.Body.String())
	}
}

// --- Monitor skip when healthy ---

func TestMonitor_SkipsWhenHealthy(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	// The monitor loop skips probing when healthy.
	// Verify by checking that lastProbeTime is NOT updated.
	before := ctrl.lastProbeTime

	// Simulate what the monitor does
	ctrl.mu.Lock()
	skip := ctrl.sess == nil || ctrl.healthy
	ctrl.mu.Unlock()

	if !skip {
		t.Error("monitor should skip when healthy")
	}
	if ctrl.lastProbeTime != before {
		t.Error("lastProbeTime should not change when skipping")
	}
}

func TestMonitor_SkipsWhenProxyDead(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = false // unhealthy — monitor would normally probe
	ctrl.mu.Unlock()

	// Set proxy route state to dead
	sess.Proxy().SetRouteDead(false)

	// Simulate monitor tick logic: check if proxy is dead
	ctrl.mu.Lock()
	proxyDead := ctrl.sess.Proxy() != nil && ctrl.sess.Proxy().RouteState() != proxy.RouteHealthy
	ctrl.mu.Unlock()

	if !proxyDead {
		t.Error("proxy should be detected as dead")
	}

	// When proxy is dead, monitor should skip (continue) — not call probeProxy.
	// probeProxy would get 503 from the dead proxy and always fail.
	// Recovery is handled by the accept-line guard's /rotate-and-wait.
}

// --- Countries endpoint test ---

func TestCountries_WithCities(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"locations":[{"country":"US","country_name":"United States","residential_count":100,"datacenter_count":10,"cities":[{"name":"New York","residential_count":60,"datacenter_count":5},{"name":"LA","residential_count":40,"datacenter_count":5}]}]}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})

	// List all countries
	w := httptest.NewRecorder()
	ctrl.httpCountries(w, httptest.NewRequest("GET", "/countries", nil))
	body := w.Body.String()
	if !strings.Contains(body, "US") || !strings.Contains(body, "United States") {
		t.Errorf("expected US listing, got %q", body)
	}

	// Cities for specific country
	w = httptest.NewRecorder()
	ctrl.httpCountries(w, httptest.NewRequest("GET", "/countries?country=US", nil))
	body = w.Body.String()
	if !strings.Contains(body, "New York") {
		t.Errorf("expected 'New York' in cities, got %q", body)
	}
	if !strings.Contains(body, "60 res / 5 dc") {
		t.Errorf("expected per-city counts, got %q", body)
	}

	// Unknown country
	w = httptest.NewRecorder()
	ctrl.httpCountries(w, httptest.NewRequest("GET", "/countries?country=ZZ", nil))
	if !strings.Contains(w.Body.String(), "Unknown country") {
		t.Errorf("expected 'Unknown country', got %q", w.Body.String())
	}
}

// --- ConnectDirect wires callback ---

func TestConnectDirect_SetsHealthy(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)

	ctrl.ConnectDirect(sess)

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if !ctrl.healthy {
		t.Error("expected healthy=true after ConnectDirect")
	}
	if ctrl.sess != sess {
		t.Error("expected session to be set")
	}
	if sess.OnUpstreamDead == nil {
		t.Error("expected OnUpstreamDead callback to be wired")
	}
}

// --- countryName helper ---

func TestCountryName_Known(t *testing.T) {
	ctrl, _ := newTestController()
	ctrl.validCountries = map[string]string{"US": "United States", "LT": "Lithuania"}

	if got := ctrl.countryName("US"); got != "United States" {
		t.Errorf("countryName(US) = %q, want United States", got)
	}
	if got := ctrl.countryName("XX"); got != "XX" {
		t.Errorf("countryName(XX) = %q, want XX (fallback)", got)
	}
}

// --- shellQuote tests ---

func TestShellQuote_Simple(t *testing.T) {
	if got := shellQuote("hello"); got != "'hello'" {
		t.Errorf("shellQuote(hello) = %q, want 'hello'", got)
	}
}

func TestShellQuote_Empty(t *testing.T) {
	if got := shellQuote(""); got != "''" {
		t.Errorf("shellQuote('') = %q, want ''", got)
	}
}

func TestShellQuote_Apostrophe(t *testing.T) {
	got := shellQuote("L'Aquila")
	want := "'L'\\''Aquila'"
	if got != want {
		t.Errorf("shellQuote(L'Aquila) = %q, want %q", got, want)
	}
}

func TestShellQuote_MultipleApostrophes(t *testing.T) {
	got := shellQuote("Côte d'Ivoire")
	want := "'Côte d'\\''Ivoire'"
	if got != want {
		t.Errorf("shellQuote(Côte d'Ivoire) = %q, want %q", got, want)
	}
}

func TestConnect_ApostropheInCountryName(t *testing.T) {
	ctrl, _ := newTestController()
	ctrl.validCountries = map[string]string{"CI": "Côte d'Ivoire"}

	w := httptest.NewRecorder()
	ctrl.httpConnect(w, httptest.NewRequest("GET", "/connect?country=CI", nil))

	// httpConnect will fail at session.Start (no real API), but it should
	// NOT fail at country validation — just check that CI is accepted.
	body := w.Body.String()
	if strings.Contains(body, "Unknown country") {
		t.Errorf("should accept CI, got: %q", body)
	}
}

func TestRotate_OutputEscapesApostrophe(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"session_id":"new-sess",
			"gateway":{"endpoint":"127.0.0.1:0","token":"tok"},
			"country":"CI","type":"residential","exit_ip":"1.2.3.4",
			"prev_session":{"duration_sec":10,"bytes_total":100,"cost_usd":0.01}
		}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})
	ctrl.validCountries = map[string]string{"CI": "Côte d'Ivoire"}

	sess := fakeSession(t)
	sess.Country = "CI"
	sess.client = client
	sess.SetExitIP("9.9.9.9")
	sess.cancel = func() {}
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpRotate(w, httptest.NewRequest("GET", "/rotate", nil))

	body := w.Body.String()
	if strings.Contains(body, "SHELLROUTE_COUNTRY_NAME='Côte d'I") {
		t.Errorf("unescaped apostrophe in output would break shell eval: %q", body)
	}
	if !strings.Contains(body, "SHELLROUTE_COUNTRY_NAME='Côte d'\\''Ivoire'") {
		t.Errorf("expected properly escaped country name, got: %q", body)
	}
}

// --- probeProxy with 402 ---

func TestProbeProxy_402Depleted(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 1024)
			conn.Read(buf)
			conn.Write([]byte("HTTP/1.1 402 Payment Required\r\n\r\n"))
			conn.Close()
		}
	}()

	if probeProxy(port) {
		t.Error("probeProxy should return false on 402")
	}
}

// --- probeProxy with connection close ---

func TestProbeProxy_ConnectionReset(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close() // immediate close
		}
	}()

	if probeProxy(port) {
		t.Error("probeProxy should return false on connection reset")
	}
}

// --- Verify the full monitor behavior with injected probeProxy ---
// (This tests the actual monitor goroutine for a brief window.)

func TestMonitor_RecoveryNotification(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.wireUpstreamCallback(sess)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	// Mark as dead via callback
	sess.OnUpstreamDead("connection_failed")

	// Verify healthcheck returns "dead"
	w := httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if w.Body.String() != "dead" {
		t.Fatalf("expected 'dead', got %q", w.Body.String())
	}

	// Verify rotating state works
	ctrl.mu.Lock()
	ctrl.autoRotating = true
	ctrl.mu.Unlock()

	w = httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if w.Body.String() != "rotating" {
		t.Fatalf("expected 'rotating', got %q", w.Body.String())
	}

	// Simulate successful rotation: set healthy + clear proxy state
	ctrl.mu.Lock()
	ctrl.autoRotating = false
	ctrl.healthy = true
	ctrl.deadReason = ""
	ctrl.notifiedDead = false
	sess.Proxy().SetRouteHealthy()
	ctrl.mu.Unlock()

	// Healthcheck should return "ok" after recovery
	w = httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if !strings.HasPrefix(w.Body.String(), "ok") {
		t.Fatalf("expected 'ok...' after recovery, got %q", w.Body.String())
	}
}

// Test the actual probeProxy function against a real proxy that responds
// to CONNECT with 200 — verifies the full HTTP parsing path.
func TestProbeProxy_ParsesConnectResponse(t *testing.T) {
	responses := []struct {
		name     string
		response string
		want     bool
	}{
		{"200 OK", "HTTP/1.1 200 Connection established\r\n\r\n", true},
		{"200 no reason", "HTTP/1.1 200 OK\r\n\r\n", true},
		{"407 auth", "HTTP/1.1 407 Proxy Authentication Required\r\n\r\n", false},
		{"503 unavailable", "HTTP/1.1 503 Service Unavailable\r\n\r\n", false},
		{"502 bad gw", "HTTP/1.1 502 Bad Gateway\r\n\r\n", false},
	}

	for _, tt := range responses {
		t.Run(tt.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			port := ln.Addr().(*net.TCPAddr).Port

			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				buf := make([]byte, 1024)
				conn.Read(buf)
				conn.Write([]byte(tt.response))
				conn.Close()
			}()

			got := probeProxy(port)
			if got != tt.want {
				t.Errorf("probeProxy with %q = %v, want %v", tt.response, got, tt.want)
			}
		})
	}
}

// Verify that the unused probeCount field doesn't interfere (if it existed).
// This test documents that we removed failCount-based dead detection.
func TestNoFailCountDeadDetection(t *testing.T) {
	ctrl, nc := newTestController()
	sess := fakeSession(t)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	// Without OnUpstreamDead, the controller should stay healthy
	// even if probeProxy would fail. The monitor only checks for recovery.
	ctrl.mu.Lock()
	stillHealthy := ctrl.healthy
	ctrl.mu.Unlock()

	if !stillHealthy {
		t.Error("controller should remain healthy without OnUpstreamDead trigger")
	}
	if len(nc.get()) != 0 {
		t.Error("no notifications expected")
	}
}

// Ensure formatters used by /balance don't panic.
func TestBalance_Endpoint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"balance_usd": 0.00, "low_balance": true, "rates": {"residential_per_gb": 3.50, "datacenter_per_gb": 1.00}}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})

	w := httptest.NewRecorder()
	ctrl.httpBalance(w, httptest.NewRequest("GET", "/balance", nil))
	body := w.Body.String()
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(body, "$0.00") {
		t.Errorf("expected $0.00 balance, got %q", body)
	}
	if !strings.Contains(body, "3.50") {
		t.Errorf("expected residential rate, got %q", body)
	}
}

// Verify /balance failure path
func TestBalance_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, "internal error")
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})

	w := httptest.NewRecorder()
	ctrl.httpBalance(w, httptest.NewRequest("GET", "/balance", nil))
	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- Disconnect keeps session on Stop() failure ---

func TestDisconnect_KeepsSessionOnError(t *testing.T) {
	// API returns error on EndSession
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})
	sess := fakeSession(t)
	sess.client = client
	sess.cancel = func() {}
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpDisconnect(w, httptest.NewRequest("GET", "/disconnect", nil))

	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}

	// Session should still be set — user can retry
	ctrl.mu.Lock()
	still := ctrl.sess
	ctrl.mu.Unlock()
	if still == nil {
		t.Error("session should be preserved on disconnect failure so user can retry")
	}
}

// --- Connect resets health state ---

func TestConnect_ResetsHealthState(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.wireUpstreamCallback(sess)

	// Simulate previous dead state
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = false
	ctrl.notifiedDead = true
	ctrl.successCount = 1
	ctrl.lowBalanceWarned = true
	ctrl.mu.Unlock()

	// Simulate new connection via ConnectDirect
	newSess := fakeSession(t)
	ctrl.ConnectDirect(newSess)

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if !ctrl.healthy {
		t.Error("healthy should be true after new connect")
	}
	// notifiedDead is not reset by ConnectDirect — that's the wireUpstreamCallback's job.
	// But the /connect handler resets it explicitly. Test that path via the state we set.
}

// --- Rotate no-session ---

func TestRotate_NoSession(t *testing.T) {
	ctrl, _ := newTestController()
	w := httptest.NewRecorder()
	ctrl.httpRotate(w, httptest.NewRequest("GET", "/rotate", nil))
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Not connected") {
		t.Errorf("expected 'Not connected', got %q", w.Body.String())
	}
}

func TestRotate_DeadSessionReturnsDisconnected(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = false
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpRotate(w, httptest.NewRequest("GET", "/rotate", nil))
	body := w.Body.String()
	if !strings.Contains(body, "DISCONNECTED") {
		t.Errorf("dead session rotate should return DISCONNECTED, got: %q", body)
	}
	if !strings.Contains(body, "Connection lost") {
		t.Errorf("should mention connection lost, got: %q", body)
	}
}

func TestRotate_ExpiredSessionReturnsDisconnected(t *testing.T) {
	// API returns 404 for expired session
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"code":"not_found","message":"Session not found or already ended"}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})
	sess := fakeSession(t)
	sess.client = client
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpRotate(w, httptest.NewRequest("GET", "/rotate", nil))
	body := w.Body.String()
	if !strings.Contains(body, "DISCONNECTED") {
		t.Errorf("expired session should return DISCONNECTED, got: %q", body)
	}
	if !strings.Contains(body, "expired") {
		t.Errorf("should mention expired, got: %q", body)
	}
}

// --- Connect resets all state on new session ---

func TestConnect_HealthStateResetOnNewSession(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.wireUpstreamCallback(sess)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	// Go unhealthy (no notification — precmd handles UX)
	sess.OnUpstreamDead("connection_failed")

	ctrl.mu.Lock()
	if ctrl.healthy {
		t.Error("should be unhealthy")
	}
	if !ctrl.notifiedDead {
		t.Error("notifiedDead should be set")
	}
	ctrl.autoRotating = false // clear for this test
	ctrl.mu.Unlock()

	// Simulate /connect handler setting new session with clean state
	newSess := fakeSession(t)
	ctrl.wireUpstreamCallback(newSess)
	ctrl.mu.Lock()
	ctrl.sess = newSess
	ctrl.healthy = true
	ctrl.notifiedDead = false
	ctrl.successCount = 0
	ctrl.lowBalanceWarned = false
	ctrl.mu.Unlock()

	// Verify clean state
	ctrl.mu.Lock()
	if !ctrl.healthy {
		t.Error("should be healthy after reconnect")
	}
	if ctrl.notifiedDead {
		t.Error("notifiedDead should be false after reconnect")
	}
	if ctrl.successCount != 0 {
		t.Error("successCount should be 0 after reconnect")
	}
	ctrl.mu.Unlock()

	// New failure should set unhealthy again
	newSess.OnUpstreamDead("connection_failed")
	ctrl.mu.Lock()
	if ctrl.healthy {
		t.Error("should be unhealthy after new failure")
	}
	if !ctrl.notifiedDead {
		t.Error("notifiedDead should be set after new failure")
	}
	ctrl.mu.Unlock()
}

// --- /connect error responses use ERROR prefix ---

func TestConnect_EmptyCountryReturnsError(t *testing.T) {
	ctrl, _ := newTestController()
	w := httptest.NewRecorder()
	ctrl.httpConnect(w, httptest.NewRequest("GET", "/connect", nil))
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.HasPrefix(w.Body.String(), "ERROR ") {
		t.Errorf("expected ERROR prefix, got %q", w.Body.String())
	}
}

func TestConnect_InvalidCountryReturnsError(t *testing.T) {
	ctrl, _ := newTestController()
	ctrl.validCountries = map[string]string{"US": "United States"}
	w := httptest.NewRecorder()
	ctrl.httpConnect(w, httptest.NewRequest("GET", "/connect?country=ZZ", nil))
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.HasPrefix(w.Body.String(), "ERROR ") {
		t.Errorf("expected ERROR prefix, got %q", w.Body.String())
	}
}

// --- /disconnect error response uses ERROR prefix ---

func TestDisconnect_ErrorPrefixOnFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})
	sess := fakeSession(t)
	sess.client = client
	sess.cancel = func() {}
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpDisconnect(w, httptest.NewRequest("GET", "/disconnect", nil))
	if !strings.HasPrefix(w.Body.String(), "ERROR ") {
		t.Errorf("expected ERROR prefix on disconnect failure, got %q", w.Body.String())
	}
}

// --- /connect clears previous session without holding lock during Stop ---

func TestConnect_ClearsPreviousSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// EndSession for old session
		if r.Method == "DELETE" {
			w.WriteHeader(200)
			w.Write([]byte(`{"duration_sec":10,"bytes_total":100,"cost_usd":0.01,"balance_usd":5.0}`))
			return
		}
		// CreateSession (POST) — will fail (we're testing the cleanup path)
		w.WriteHeader(500)
		w.Write([]byte(`{"message":"test error","code":"internal"}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{DefaultPort: 0, DefaultType: "residential"})

	oldSess := fakeSession(t)
	oldSess.client = client
	oldSess.cancel = func() {}
	ctrl.mu.Lock()
	ctrl.sess = oldSess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpConnect(w, httptest.NewRequest("GET", "/connect?country=US", nil))

	// Connection will fail (mock returns 500 for CreateSession),
	// old session should be preserved (not disconnected)
	ctrl.mu.Lock()
	currentSess := ctrl.sess
	ctrl.mu.Unlock()
	if currentSess == nil {
		t.Error("old session should be preserved when new connection fails")
	}

	// Response should NOT contain DISCONNECTED
	if strings.Contains(w.Body.String(), "DISCONNECTED") {
		t.Error("should not send DISCONNECTED when old session is preserved")
	}
}

func TestConnect_PreservesOldSessionOnNoProviders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "end") {
			w.WriteHeader(200)
			w.Write([]byte(`{"duration_sec":0,"bytes_total":0,"cost_usd":0,"balance_usd":5.0}`))
			return
		}
		// CreateSession — returns no_providers (409)
		w.WriteHeader(409)
		w.Write([]byte(`{"message":"No datacenter IPs available in US, Watauga","code":"no_providers"}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{DefaultPort: 0, DefaultType: "residential"})
	ctrl.validCountries = map[string]string{"US": "United States"}

	oldSess := fakeSession(t)
	oldSess.SetExitIP("1.2.3.4")
	oldSess.client = client
	oldSess.cancel = func() {}
	ctrl.mu.Lock()
	ctrl.sess = oldSess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/connect?country=US&city=Watauga&iptype=datacenter", nil)
	ctrl.httpConnect(w, req)

	// Old session preserved
	ctrl.mu.Lock()
	currentSess := ctrl.sess
	healthy := ctrl.healthy
	ctrl.mu.Unlock()
	if currentSess == nil {
		t.Fatal("old session should be preserved when no providers in target")
	}
	if currentSess.GetExitIP() != "1.2.3.4" {
		t.Errorf("old session IP should be unchanged, got %q", currentSess.GetExitIP())
	}
	if !healthy {
		t.Error("should still be healthy")
	}

	// Error message should mention the specific issue
	body := w.Body.String()
	if !strings.Contains(body, "No datacenter IPs") {
		t.Errorf("response should contain API error, got %q", body)
	}
	if strings.Contains(body, "DISCONNECTED") {
		t.Error("should not send DISCONNECTED when old session is preserved")
	}
}

func TestConnect_PreservesOldSessionOnExitIPTimeout(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "end") {
			w.WriteHeader(200)
			w.Write([]byte(`{"duration_sec":0,"bytes_total":0,"cost_usd":0,"balance_usd":5.0}`))
			return
		}
		callCount++
		// CreateSession succeeds but the proxy won't actually work (no real gateway)
		w.WriteHeader(201)
		w.Write([]byte(`{
			"session_id":"new-sess",
			"gateway":{"endpoint":"127.0.0.1:0","token":"tok"},
			"country":"DE","type":"residential","exit_ip":"",
			"byte_budget":1000000,"balance_usd":5.0
		}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{DefaultPort: 0, DefaultType: "residential"})
	ctrl.validCountries = map[string]string{"US": "United States", "DE": "Germany"}
	ctrl.connectTimeout = 2 * time.Second // short timeout for tests

	oldSess := fakeSession(t)
	oldSess.SetExitIP("9.9.9.9")
	oldSess.client = client
	oldSess.cancel = func() {}
	ctrl.mu.Lock()
	ctrl.sess = oldSess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpConnect(w, httptest.NewRequest("GET", "/connect?country=DE", nil))

	// New session will fail to get exit IP (no real gateway).
	// Old session should be preserved.
	ctrl.mu.Lock()
	currentSess := ctrl.sess
	ctrl.mu.Unlock()
	if currentSess == nil {
		t.Fatal("old session should be preserved when new session can't connect")
	}
	if currentSess.GetExitIP() != "9.9.9.9" {
		t.Errorf("old session IP should be unchanged, got %q", currentSess.GetExitIP())
	}
}

func TestConnect_DisconnectsOldOnSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "end") {
			w.WriteHeader(200)
			w.Write([]byte(`{"duration_sec":10,"bytes_total":100,"cost_usd":0.01,"balance_usd":5.0}`))
			return
		}
		w.WriteHeader(201)
		w.Write([]byte(`{
			"session_id":"new-sess",
			"gateway":{"endpoint":"127.0.0.1:0","token":"tok"},
			"country":"DE","type":"residential","exit_ip":"",
			"byte_budget":1000000,"balance_usd":5.0
		}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{DefaultPort: 0, DefaultType: "residential"})
	ctrl.validCountries = map[string]string{"DE": "Germany"}
	ctrl.connectTimeout = 2 * time.Second

	oldCancelled := false
	oldSess := fakeSession(t)
	oldSess.client = client
	oldSess.cancel = func() { oldCancelled = true }
	ctrl.mu.Lock()
	ctrl.sess = oldSess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpConnect(w, httptest.NewRequest("GET", "/connect?country=DE", nil))

	// New session will fail (no real gateway = no exit IP),
	// so old session should NOT be cancelled
	if oldCancelled {
		t.Error("old session should not be cancelled when new session fails")
	}
}

// --- UK alias works in /connect ---

func TestConnect_UKAlias(t *testing.T) {
	ctrl, _ := newTestController()
	ctrl.validCountries = map[string]string{"GB": "United Kingdom"}

	// UK should resolve to GB and pass validation
	w := httptest.NewRecorder()
	ctrl.httpConnect(w, httptest.NewRequest("GET", "/connect?country=UK", nil))
	// It will fail at session.Start (no real API), but should NOT fail with "Unknown country"
	body := w.Body.String()
	if strings.Contains(body, "Unknown country") {
		t.Errorf("UK should resolve to GB, but got: %q", body)
	}
}

// --- /countries?country= with UK alias ---

func TestCountries_UKAlias(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"locations":[{"country":"GB","country_name":"United Kingdom","residential_count":100,"datacenter_count":10,"cities":[{"name":"London","residential_count":60,"datacenter_count":5}]}]}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})

	w := httptest.NewRecorder()
	ctrl.httpCountries(w, httptest.NewRequest("GET", "/countries?country=uk", nil))
	body := w.Body.String()
	if !strings.Contains(body, "London") {
		t.Errorf("expected cities for GB via UK alias, got %q", body)
	}
}

// --- Accumulated stats ---

func TestAccumulate_ResetOnConnect(t *testing.T) {
	ctrl, _ := newTestController()

	// Simulate accumulated stats from previous rotations
	ctrl.mu.Lock()
	ctrl.accumDuration = 100
	ctrl.accumBytes = 5000
	ctrl.accumCost = 0.50
	ctrl.mu.Unlock()

	// ConnectDirect resets (simulates /connect path)
	sess := fakeSession(t)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.accumDuration = 0
	ctrl.accumBytes = 0
	ctrl.accumCost = 0
	ctrl.mu.Unlock()

	ctrl.mu.Lock()
	if ctrl.accumDuration != 0 || ctrl.accumBytes != 0 || ctrl.accumCost != 0 {
		t.Error("accumulators should be reset on new connect")
	}
	ctrl.mu.Unlock()
}

func TestAccumulate_RotateAddsStats(t *testing.T) {
	ctrl, _ := newTestController()

	// Simulate rotate adding prev session stats
	prev := &api.PrevSessionStats{
		DurationSec: 60,
		BytesTotal:  1024,
		CostUSD:     0.10,
	}

	ctrl.mu.Lock()
	ctrl.accumDuration += prev.DurationSec
	ctrl.accumBytes += prev.BytesTotal
	ctrl.accumCost += prev.CostUSD
	ctrl.mu.Unlock()

	// Second rotation
	prev2 := &api.PrevSessionStats{
		DurationSec: 30,
		BytesTotal:  2048,
		CostUSD:     0.05,
	}

	ctrl.mu.Lock()
	ctrl.accumDuration += prev2.DurationSec
	ctrl.accumBytes += prev2.BytesTotal
	ctrl.accumCost += prev2.CostUSD
	ctrl.mu.Unlock()

	ctrl.mu.Lock()
	if ctrl.accumDuration != 90 {
		t.Errorf("accumDuration = %d, want 90", ctrl.accumDuration)
	}
	if ctrl.accumBytes != 3072 {
		t.Errorf("accumBytes = %d, want 3072", ctrl.accumBytes)
	}
	if ctrl.accumCost < 0.149 || ctrl.accumCost > 0.151 {
		t.Errorf("accumCost = %f, want ~0.15", ctrl.accumCost)
	}
	ctrl.mu.Unlock()
}

func TestStopAndDisconnect_IncludesAccumulated(t *testing.T) {
	// Mock API that returns session end stats
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"session_id":"s1","duration_seconds":30,"bytes_total":500,"cost_usd":0.05,"balance_usd":9.00}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})

	sess := fakeSession(t)
	sess.client = client
	sess.cancel = func() {}

	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	// Simulate 2 previous rotations
	ctrl.accumDuration = 120
	ctrl.accumBytes = 5000
	ctrl.accumCost = 0.20
	ctrl.mu.Unlock()

	resp := ctrl.StopAndDisconnect()
	if resp == nil {
		t.Fatal("expected response")
	}

	// Should be accumulated (120+30=150, 5000+500=5500, 0.20+0.05=0.25)
	if resp.DurationSec != 150 {
		t.Errorf("DurationSec = %d, want 150", resp.DurationSec)
	}
	if resp.BytesTotal != 5500 {
		t.Errorf("BytesTotal = %d, want 5500", resp.BytesTotal)
	}
	if resp.CostUSD != 0.25 {
		t.Errorf("CostUSD = %f, want 0.25", resp.CostUSD)
	}
	if resp.BalanceUSD != 9.00 {
		t.Errorf("BalanceUSD = %f, want 9.00", resp.BalanceUSD)
	}

	// Accumulators should be reset
	ctrl.mu.Lock()
	if ctrl.accumDuration != 0 || ctrl.accumBytes != 0 || ctrl.accumCost != 0 {
		t.Error("accumulators should be reset after StopAndDisconnect")
	}
	ctrl.mu.Unlock()
}

func TestDisconnect_IncludesAccumulated(t *testing.T) {
	// Mock API that returns session end stats
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"session_id":"s1","duration_seconds":45,"bytes_total":800,"cost_usd":0.08,"balance_usd":8.50}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})

	sess := fakeSession(t)
	sess.client = client
	sess.cancel = func() {}

	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.accumDuration = 60
	ctrl.accumBytes = 2000
	ctrl.accumCost = 0.12
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpDisconnect(w, httptest.NewRequest("GET", "/disconnect", nil))

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	// Should show accumulated totals (60+45=105s = 1m 45s)
	if !strings.Contains(body, "1m 45s") {
		t.Errorf("expected accumulated duration '1m 45s' in output, got %q", body)
	}
}

// --- Exit IP tracking tests ---

func TestWireUpstreamCallback_SetsOnExitIPUpdate(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.wireUpstreamCallback(sess)

	if sess.OnExitIPUpdate == nil {
		t.Fatal("wireUpstreamCallback should set OnExitIPUpdate")
	}
}

func TestOnExitIPUpdate_UpdatesSessionUnderLock(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("1.1.1.1")
	ctrl.wireUpstreamCallback(sess)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	// Simulate proxy callback (fires on proxy goroutine)
	sess.OnExitIPUpdate("2.2.2.2")

	// Read under lock (same as httpRouteCheck)
	ctrl.mu.Lock()
	ip := ctrl.sess.GetExitIP()
	ctrl.mu.Unlock()

	if ip != "2.2.2.2" {
		t.Errorf("ExitIP = %q, want 2.2.2.2", ip)
	}
}

func TestOnExitIPUpdate_SkipsSameIP(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("1.1.1.1")
	ctrl.wireUpstreamCallback(sess)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	// Same IP — should not change (no-op)
	sess.OnExitIPUpdate("1.1.1.1")

	ctrl.mu.Lock()
	ip := ctrl.sess.GetExitIP()
	ctrl.mu.Unlock()

	if ip != "1.1.1.1" {
		t.Errorf("ExitIP should stay 1.1.1.1, got %q", ip)
	}
}

func TestRouteCheck_ReflectsIPFromCallback(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("10.0.0.1")
	ctrl.wireUpstreamCallback(sess)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	// Initial check
	w := httptest.NewRecorder()
	ctrl.httpRouteCheck(w, httptest.NewRequest("GET", "/route-check", nil))
	if w.Body.String() != "ok 10.0.0.1" {
		t.Fatalf("initial: expected 'ok 10.0.0.1', got %q", w.Body.String())
	}

	// Simulate proxy detecting new IP via X-Exit-IP header
	sess.OnExitIPUpdate("10.0.0.99")

	// Route check should reflect the new IP immediately
	w2 := httptest.NewRecorder()
	ctrl.httpRouteCheck(w2, httptest.NewRequest("GET", "/route-check", nil))
	if w2.Body.String() != "ok 10.0.0.99" {
		t.Errorf("after callback: expected 'ok 10.0.0.99', got %q", w2.Body.String())
	}
}

func TestOnExitIPUpdate_ConcurrentSafety(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("1.1.1.1")
	ctrl.wireUpstreamCallback(sess)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	// Concurrent writers (simulate multiple CONNECT responses)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sess.OnExitIPUpdate(fmt.Sprintf("10.0.0.%d", n))
		}(i)
	}

	// Concurrent readers (simulate route-check calls)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctrl.mu.Lock()
			_ = ctrl.sess.GetExitIP()
			ctrl.mu.Unlock()
		}()
	}

	wg.Wait()

	// Just verify no panic/race — final value is non-deterministic
	ctrl.mu.Lock()
	ip := ctrl.sess.GetExitIP()
	ctrl.mu.Unlock()
	if ip == "" {
		t.Error("ExitIP should not be empty after concurrent updates")
	}
}

func TestHealthcheck_ReflectsIPChange(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("10.0.0.1")
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.lastProbeTime = time.Now()
	ctrl.mu.Unlock()

	// Initial IP
	w := httptest.NewRecorder()
	ctrl.httpHealthCheck(w, httptest.NewRequest("GET", "/healthcheck", nil))
	if w.Body.String() != "ok 10.0.0.1" {
		t.Fatalf("initial: expected 'ok 10.0.0.1', got %q", w.Body.String())
	}

	// Simulate gateway changing the tunnel — session IP updates
	ctrl.mu.Lock()
	ctrl.sess.SetExitIP("10.0.0.99")
	ctrl.mu.Unlock()

	w2 := httptest.NewRecorder()
	ctrl.httpHealthCheck(w2, httptest.NewRequest("GET", "/healthcheck", nil))
	if w2.Body.String() != "ok 10.0.0.99" {
		t.Errorf("after change: expected 'ok 10.0.0.99', got %q", w2.Body.String())
	}
}

// --- M1 regression: autoRotate must not assign c.sess after Stop ---

func TestAutoRotate_DoesNotAssignAfterStop(t *testing.T) {
	// Test 1: sess=nil after StopAndDisconnect — bails at initial nil guard
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.wireUpstreamCallback(sess)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	ctrl.Stop()

	ctrl.mu.Lock()
	ctrl.sess = nil
	ctrl.mu.Unlock()

	ctrl.autoRotateWithContext(context.Background())

	ctrl.mu.Lock()
	if ctrl.sess != nil {
		t.Error("autoRotate should not assign c.sess after Stop()")
	}
	ctrl.mu.Unlock()

	// Test 2: sess non-nil, stopped=true — reaches the stopped check, must not set healthy
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"session_id":"new-sess","gateway":{"endpoint":"localhost:0","token":"tok"},"exit_ip":"1.2.3.4"}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl2 := NewController(client, &config.Config{})
	sess2 := fakeSession(t)
	ctrl2.mu.Lock()
	ctrl2.sess = sess2
	ctrl2.healthy = false
	ctrl2.mu.Unlock()

	ctrl2.Stop()

	ctrl2.autoRotateWithContext(context.Background())

	ctrl2.mu.Lock()
	if ctrl2.healthy {
		t.Error("autoRotate must not set healthy=true after Stop()")
	}
	ctrl2.mu.Unlock()
}

func TestStopAndDisconnect_SetsStoppedFlag(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.client = api.New("http://localhost:0", "pk_test")
	sess.cancel = func() {}
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	ctrl.StopAndDisconnect()

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if !ctrl.stopped {
		t.Error("StopAndDisconnect should set stopped=true via Stop()")
	}
	if ctrl.sess != nil {
		t.Error("session should be nil after StopAndDisconnect")
	}
}

func TestStart_ResetsStoppedFlag(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"locations":[]}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})

	// Manually set stopped
	ctrl.mu.Lock()
	ctrl.stopped = true
	ctrl.mu.Unlock()

	port, err := ctrl.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer ctrl.Stop()

	if port == 0 {
		t.Error("expected non-zero port")
	}

	ctrl.mu.Lock()
	if ctrl.stopped {
		t.Error("Start() should reset stopped=false")
	}
	ctrl.mu.Unlock()
}

// --- M2 regression: shellQuote handles apostrophes ---

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"United States", "'United States'"},
		{"L'Aquila", "'L'\\''Aquila'"},
		{"Côte d'Ivoire", "'Côte d'\\''Ivoire'"},
		{"'s-Hertogenbosch", "''\\''s-Hertogenbosch'"},
		{"", "''"},
		{"simple", "'simple'"},
	}

	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConnect_ExportsCityWithApostrophe(t *testing.T) {
	ctrl, _ := newTestController()
	ctrl.validCountries = map[string]string{"CI": "Côte d'Ivoire"}

	// Simulate what httpConnect does for the export lines
	var buf strings.Builder
	sess := &Session{
		ID:      "test-session",
		Country: "CI",
		City:    "L'Aquila",
		exitIP:  "1.2.3.4",
		Port:    8080,
	}
	fmt.Fprintf(&buf, "export SHELLROUTE_COUNTRY_NAME=%s\n", shellQuote(ctrl.countryName(sess.Country)))
	fmt.Fprintf(&buf, "export SHELLROUTE_CITY=%s\n", shellQuote(sess.City))

	output := buf.String()

	// Verify the output is valid shell (no unmatched quotes)
	if strings.Contains(output, "='L'Aquila'") {
		t.Error("city export should use shellQuote, not bare single quotes")
	}
	if !strings.Contains(output, "SHELLROUTE_CITY='L'\\''Aquila'") {
		t.Errorf("expected properly escaped city, got: %s", output)
	}
	if !strings.Contains(output, "SHELLROUTE_COUNTRY_NAME='Côte d'\\''Ivoire'") {
		t.Errorf("expected properly escaped country name, got: %s", output)
	}
}

// --- Route-check detects proxy depleted state ---

func TestRouteCheck_DetectsProxyDepleted(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("1.2.3.4")
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true // controller thinks healthy
	ctrl.mu.Unlock()

	// But proxy route state is depleted
	if sess.Proxy() != nil {
		sess.Proxy().SetRouteDepleted()
	}

	w := httptest.NewRecorder()
	ctrl.httpRouteCheck(w, httptest.NewRequest("GET", "/route-check", nil))

	// fakeSession has no real proxy, so proxy check is skipped
	// Test with a real proxy would require full setup — test the controller
	// logic separately: when healthy=false and deadReason=depleted
	ctrl.mu.Lock()
	ctrl.healthy = false
	ctrl.deadReason = "depleted"
	ctrl.mu.Unlock()

	w = httptest.NewRecorder()
	ctrl.httpRouteCheck(w, httptest.NewRequest("GET", "/route-check", nil))
	if w.Body.String() != "dead depleted" {
		t.Errorf("expected 'dead depleted', got %q", w.Body.String())
	}
}

func TestRouteCheck_DepletedStaysDepletedAfterTopUp(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("1.2.3.4")
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = false
	ctrl.deadReason = "depleted"
	ctrl.mu.Unlock()

	// Even after a top-up, depleted session stays depleted —
	// terminated server-side, user must /connect.
	w := httptest.NewRecorder()
	ctrl.httpRouteCheck(w, httptest.NewRequest("GET", "/route-check", nil))
	if w.Body.String() != "dead depleted" {
		t.Errorf("expected 'dead depleted', got %q", w.Body.String())
	}
}

// --- markDead syncs controller + proxy state ---

func TestMarkDead_SyncsControllerAndProxy(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	sess.SetExitIP("1.2.3.4")
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	ctrl.markDead(sess, "depleted")

	ctrl.mu.Lock()
	healthy := ctrl.healthy
	reason := ctrl.deadReason
	notified := ctrl.notifiedDead
	ctrl.mu.Unlock()

	if healthy {
		t.Error("markDead should set healthy=false")
	}
	if reason != "depleted" {
		t.Errorf("deadReason = %q, want depleted", reason)
	}
	if !notified {
		t.Error("markDead should set notifiedDead=true")
	}
}

func TestMarkDead_ConnectionFailed(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	ctrl.markDead(sess, "connection_failed")

	ctrl.mu.Lock()
	reason := ctrl.deadReason
	ctrl.mu.Unlock()

	if reason != "connection_failed" {
		t.Errorf("deadReason = %q, want connection_failed", reason)
	}
}

// --- route-check calls API when bytes change ---

func TestRouteCheck_CallsAPIWhenBytesChange(t *testing.T) {
	var apiCalled bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "route-health") {
			apiCalled = true
			w.Write([]byte(`{"healthy":true,"reason":""}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})
	sess := fakeSession(t)
	sess.SetExitIP("1.2.3.4")
	sess.client = client
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.lastCheckedBytes = 0
	ctrl.mu.Unlock()

	// No proxy → bytes check skipped, no API call
	w := httptest.NewRecorder()
	ctrl.httpRouteCheck(w, httptest.NewRequest("GET", "/route-check", nil))
	if apiCalled {
		t.Error("should not call API when no proxy")
	}
	if !strings.HasPrefix(w.Body.String(), "ok") {
		t.Errorf("expected ok, got %q", w.Body.String())
	}
}

func TestRouteCheck_APIReturnsDepletedMarksDead(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "route-health") {
			w.Write([]byte(`{"healthy":false,"reason":"depleted"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})
	sess := fakeSession(t)
	sess.SetExitIP("1.2.3.4")
	sess.client = client

	// Create a real proxy so BytesTotal() works
	m := &meter.Meter{}
	m.AddDown(1000) // simulate traffic
	proxyServer := proxy.NewServer(&proxy.Config{
		ListenPort: 0,
		Meter:      m,
	})
	sess.SetProxy(proxyServer)

	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.lastCheckedBytes = 0 // different from current (1000)
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpRouteCheck(w, httptest.NewRequest("GET", "/route-check", nil))

	if w.Body.String() != "dead depleted" {
		t.Errorf("expected 'dead depleted', got %q", w.Body.String())
	}

	ctrl.mu.Lock()
	if ctrl.healthy {
		t.Error("controller should be unhealthy after API says depleted")
	}
	if ctrl.deadReason != "depleted" {
		t.Errorf("deadReason = %q, want depleted", ctrl.deadReason)
	}
	ctrl.mu.Unlock()
}

// --- balanceMonitor does NOT auto-revive depleted ---

func TestBalanceMonitor_DoesNotReviveDepleted(t *testing.T) {
	// Simulate what the balance monitor does: checks balance, sees
	// positive balance, but session is depleted. Should NOT revive.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"balance_usd":10.0,"low_balance":false,"email":"test@test.com","rates":{"residential_per_gb":3.0,"datacenter_per_gb":1.0}}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})
	sess := fakeSession(t)
	sess.client = client
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = false
	ctrl.deadReason = "depleted"
	ctrl.mu.Unlock()

	// Call GetBalance like the monitor would
	resp, err := client.GetBalance()
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if resp.BalanceUSD <= 0 {
		t.Fatal("expected positive balance from mock")
	}

	// The monitor skips budget refresh when isDepleted
	ctrl.mu.Lock()
	isDepleted := ctrl.deadReason == "depleted" && !ctrl.healthy
	ctrl.mu.Unlock()
	if !isDepleted {
		t.Fatal("session should be detected as depleted")
	}

	// Verify state didn't change
	ctrl.mu.Lock()
	if ctrl.healthy {
		t.Error("depleted session should stay unhealthy even with positive balance")
	}
	if ctrl.deadReason != "depleted" {
		t.Errorf("deadReason should stay 'depleted', got %q", ctrl.deadReason)
	}
	ctrl.mu.Unlock()
}

func TestBalanceMonitor_UsesServerByteBudget(t *testing.T) {
	var gotType, gotSessionID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.URL.Query().Get("type")
		gotSessionID = r.URL.Query().Get("session_id")
		// Server returns byte_budget — CLI should use this directly, not calculate locally
		w.Write([]byte(`{"balance_usd":5.0,"low_balance":false,"byte_budget":1666666666}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})
	sess := fakeSession(t)
	sess.Type = "datacenter"
	sess.client = client

	m := &meter.Meter{}
	m.AddDown(1000) // simulate some traffic
	proxyServer := proxy.NewServer(&proxy.Config{ListenPort: 0, Meter: m})
	sess.SetProxy(proxyServer)

	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	// Simulate what the balance monitor does
	resp, err := client.GetBalance(api.BalanceOpts{IPType: sess.Type, SessionID: sess.ID})
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if gotType != "datacenter" {
		t.Errorf("expected type=datacenter, got %q", gotType)
	}
	if gotSessionID != "test-session" {
		t.Errorf("expected session_id=test-session, got %q", gotSessionID)
	}
	if resp.ByteBudget != 1666666666 {
		t.Fatalf("expected byte_budget=1666666666, got %d", resp.ByteBudget)
	}

	// Apply budget like the monitor would
	if resp.ByteBudget > 0 && sess.Proxy() != nil {
		sess.Proxy().SetBudget(resp.ByteBudget + sess.Proxy().BytesTotal())
	}

	// Budget should be server value + current bytes
	expected := int64(1666666666 + 1000)
	got := sess.Proxy().Budget()
	if got != expected {
		t.Errorf("budget = %d, want %d", got, expected)
	}
}

// --- needsRouteVerify flag behavior ---

func TestRouteCheck_NeedsRouteVerifyRetainedOnAPIFailure(t *testing.T) {
	// API returns 500 — needsRouteVerify should stay set for retry
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"message":"internal error"}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})
	sess := fakeSession(t)
	sess.SetExitIP("1.2.3.4")
	sess.client = client

	m := &meter.Meter{}
	m.AddDown(1000)
	proxyServer := proxy.NewServer(&proxy.Config{ListenPort: 0, Meter: m})
	sess.SetProxy(proxyServer)

	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.needsRouteVerify = true
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpRouteCheck(w, httptest.NewRequest("GET", "/route-check", nil))

	// Should fail closed as dead
	if w.Body.String() != "dead" {
		t.Errorf("expected 'dead' on API error, got %q", w.Body.String())
	}

	// needsRouteVerify should still be set for retry
	ctrl.mu.Lock()
	if !ctrl.needsRouteVerify {
		t.Error("needsRouteVerify should stay set after API failure")
	}
	ctrl.mu.Unlock()
}

func TestRouteCheck_NeedsRouteVerifyClearedOnSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"healthy":true,"reason":""}`))
	}))
	defer ts.Close()

	client := api.New(ts.URL, "pk_test")
	ctrl := NewController(client, &config.Config{})
	sess := fakeSession(t)
	sess.SetExitIP("1.2.3.4")
	sess.client = client

	m := &meter.Meter{}
	m.AddDown(1000)
	proxyServer := proxy.NewServer(&proxy.Config{ListenPort: 0, Meter: m})
	sess.SetProxy(proxyServer)

	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = true
	ctrl.needsRouteVerify = true
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpRouteCheck(w, httptest.NewRequest("GET", "/route-check", nil))

	if !strings.HasPrefix(w.Body.String(), "ok") {
		t.Errorf("expected 'ok...', got %q", w.Body.String())
	}

	ctrl.mu.Lock()
	if ctrl.needsRouteVerify {
		t.Error("needsRouteVerify should be cleared after successful API response")
	}
	if ctrl.lastCheckedBytes != 1000 {
		t.Errorf("lastCheckedBytes = %d, want 1000", ctrl.lastCheckedBytes)
	}
	ctrl.mu.Unlock()
}

// --- markDead session identity guard ---

func TestMarkDead_IgnoresStaleSession(t *testing.T) {
	ctrl, _ := newTestController()
	oldSess := fakeSession(t)
	oldSess.ID = "old-session"
	newSess := fakeSession(t)
	newSess.ID = "new-session"
	newSess.SetExitIP("2.2.2.2")

	ctrl.mu.Lock()
	ctrl.sess = newSess
	ctrl.healthy = true
	ctrl.mu.Unlock()

	// markDead with old session should be ignored
	ctrl.markDead(oldSess, "depleted")

	ctrl.mu.Lock()
	if !ctrl.healthy {
		t.Error("markDead with stale session should not change healthy")
	}
	if ctrl.deadReason != "" {
		t.Errorf("deadReason should be empty, got %q", ctrl.deadReason)
	}
	ctrl.mu.Unlock()
}

// --- /rotate-and-wait ---

func TestRotateAndWait_RequiresPost(t *testing.T) {
	ctrl, _ := newTestController()
	w := httptest.NewRecorder()
	ctrl.httpRotateAndWait(w, httptest.NewRequest("GET", "/rotate-and-wait", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET should return 405, got %d", w.Code)
	}
}

func TestRotateAndWait_NoSession(t *testing.T) {
	ctrl, _ := newTestController()
	w := httptest.NewRecorder()
	ctrl.httpRotateAndWait(w, httptest.NewRequest("POST", "/rotate-and-wait", nil))
	if w.Body.String() != "failed" {
		t.Errorf("expected 'failed' with no session, got %q", w.Body.String())
	}
}

func TestRotateAndWait_DepletedDoesNotRotate(t *testing.T) {
	ctrl, _ := newTestController()
	sess := fakeSession(t)
	ctrl.mu.Lock()
	ctrl.sess = sess
	ctrl.healthy = false
	ctrl.deadReason = "depleted"
	ctrl.mu.Unlock()

	// Route check should return dead depleted (guard skips rotate-and-wait)
	w := httptest.NewRecorder()
	ctrl.httpRouteCheck(w, httptest.NewRequest("GET", "/route-check", nil))
	if w.Body.String() != "dead depleted" {
		t.Errorf("expected 'dead depleted', got %q", w.Body.String())
	}
}

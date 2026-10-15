package session

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/proxy/proxytest"
)

// ownController returns a controller whose config holds two saved upstreams:
// "work" (a fake HTTP proxy with auth) and "dead" (nothing listening). The
// API client points at a closed port: any API call would fail.
func ownController(t *testing.T) (*Controller, *proxytest.HTTPProxy) {
	t.Helper()
	t.Setenv("SHELLROUTE_HOME", t.TempDir())
	p := proxytest.StartHTTPProxy(t, "u", "p")
	cfg := &config.Config{}
	cfg.AddUpstream(config.Upstream{Name: "work", URL: "http://u:p@" + p.Addr})
	cfg.AddUpstream(config.Upstream{Name: "dead", URL: "http://127.0.0.1:1"})
	ctrl := NewController(api.New("http://127.0.0.1:1", ""), cfg)
	withExitIPEndpoints(t, echoIP(t, `{"ip": "203.0.113.4", "country": "DE"}`).URL)
	t.Cleanup(func() { ctrl.StopAndDisconnect() })
	return ctrl, p
}

func connectOwn(t *testing.T, ctrl *Controller, name string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	ctrl.httpConnect(w, httptest.NewRequest("GET", "/connect?upstream="+name, nil))
	return w.Code, w.Body.String()
}

func TestConnectOwn_ExportsWithoutAPI(t *testing.T) {
	ctrl, p := ownController(t)
	code, body := connectOwn(t, ctrl, "work")
	if code != 200 {
		t.Fatalf("code %d: %s", code, body)
	}
	for _, want := range []string{
		"export HTTP_PROXY=http://127.0.0.1:",
		"export HTTPS_PROXY=http://127.0.0.1:",
		"export SHELLROUTE_SESSION_ID=own-work",
		"export SHELLROUTE_UPSTREAM=work",
		"export SHELLROUTE_COUNTRY=DE",
		"export SHELLROUTE_EXIT_IP=203.0.113.4",
		"export SHELLROUTE_PORT=",
		"NODE_USE_ENV_PROXY",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exports lack %q:\n%s", want, body)
		}
	}
	for _, never := range []string{"LOW_BALANCE", "u:p@", "ERROR"} {
		if strings.Contains(body, never) {
			t.Errorf("exports must not contain %q:\n%s", never, body)
		}
	}
	sess := ctrl.Session()
	if sess == nil || !sess.Own || sess.UpstreamName != "work" {
		t.Fatalf("session = %+v", sess)
	}
	if p.Requests.Load() == 0 {
		t.Error("the exit IP probe should have gone through the proxy")
	}
}

func TestConnectOwn_UnknownName(t *testing.T) {
	ctrl, _ := ownController(t)
	code, body := connectOwn(t, ctrl, "nope")
	if code != 400 || !strings.Contains(body, "ERROR Unknown upstream \"nope\"") {
		t.Errorf("code %d body %q", code, body)
	}
	if ctrl.Session() != nil {
		t.Error("no session should exist")
	}
}

func TestConnectOwn_TunnelFailureKeepsOldSession(t *testing.T) {
	ctrl, _ := ownController(t)
	if code, body := connectOwn(t, ctrl, "work"); code != 200 {
		t.Fatalf("first connect: %d %s", code, body)
	}
	_, body := connectOwn(t, ctrl, "dead")
	if !strings.Contains(body, "ERROR Cannot connect through dead: connection refused") {
		t.Errorf("body = %q", body)
	}
	if s := ctrl.Session(); s == nil || s.UpstreamName != "work" {
		t.Error("the working session must survive a failed switch")
	}
}

func TestHostedExports_ClearUpstreamVariable(t *testing.T) {
	ctrl, _ := newTestController()
	w := httptest.NewRecorder()
	ctrl.writeConnectExports(w, fakeSession(t))
	if !strings.Contains(w.Body.String(), "unset SHELLROUTE_UPSTREAM") {
		t.Error("switching to a hosted route must clear SHELLROUTE_UPSTREAM")
	}
}

func TestRotateOwn_ProviderControlsRotation(t *testing.T) {
	ctrl, _ := ownController(t)
	connectOwn(t, ctrl, "work")
	w := httptest.NewRecorder()
	ctrl.httpRotate(w, httptest.NewRequest("GET", "/rotate", nil))
	if !strings.Contains(w.Body.String(), "NO_OTHER_IPS=Rotate is only for Shellroute routes") {
		t.Errorf("body = %q", w.Body.String())
	}
	if ctrl.Session() == nil || !ctrl.Session().Own {
		t.Error("rotate must leave the own session in place")
	}
}

func TestRotateAndWaitOwn_Fails(t *testing.T) {
	ctrl, _ := ownController(t)
	connectOwn(t, ctrl, "work")
	w := httptest.NewRecorder()
	ctrl.httpRotateAndWait(w, httptest.NewRequest("POST", "/rotate-and-wait", nil))
	if w.Body.String() != "failed" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestRouteCheckOwn_UsesLocalStateOnly(t *testing.T) {
	ctrl, _ := ownController(t)
	connectOwn(t, ctrl, "work")
	ctrl.mu.Lock()
	ctrl.needsRouteVerify = true // would trigger an API call for a hosted route
	ctrl.mu.Unlock()

	w := httptest.NewRecorder()
	ctrl.httpRouteCheck(w, httptest.NewRequest("GET", "/route-check", nil))
	if w.Body.String() != "ok 203.0.113.4" {
		t.Errorf("route-check = %q, want ok with the exit IP (the API is unreachable)", w.Body.String())
	}

	ctrl.Session().Proxy().SetRouteDead(false)
	w = httptest.NewRecorder()
	ctrl.httpRouteCheck(w, httptest.NewRequest("GET", "/route-check", nil))
	if w.Body.String() != "dead" {
		t.Errorf("route-check after local death = %q, want dead", w.Body.String())
	}
}

func TestStatusOwn_ShowsUpstreamNotPassword(t *testing.T) {
	ctrl, _ := ownController(t)
	connectOwn(t, ctrl, "work")
	w := httptest.NewRecorder()
	ctrl.httpStatus(w, httptest.NewRequest("GET", "/status", nil))
	body := w.Body.String()
	for _, want := range []string{"Upstream: work", "Via:      http://u:***@", "Exit IP:  203.0.113.4", "Country:  DE (observed)", "Health:   healthy"} {
		if !strings.Contains(body, want) {
			t.Errorf("status lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "u:p@") {
		t.Errorf("status leaks the password:\n%s", body)
	}
}

func TestDisconnectOwn_NoCostLines(t *testing.T) {
	ctrl, _ := ownController(t)
	connectOwn(t, ctrl, "work")
	w := httptest.NewRecorder()
	ctrl.httpDisconnect(w, httptest.NewRequest("GET", "/disconnect", nil))
	body := w.Body.String()
	if !strings.Contains(body, "Duration:") || !strings.Contains(body, "Data used:") {
		t.Errorf("summary lacks duration/data:\n%s", body)
	}
	if strings.Contains(body, "Cost:") || strings.Contains(body, "Balance:") {
		t.Errorf("own route has nothing to bill:\n%s", body)
	}
	if ctrl.Session() != nil {
		t.Error("session should be cleared")
	}
}

func TestStopAndDisconnectOwn_ReportsOwn(t *testing.T) {
	ctrl, _ := ownController(t)
	connectOwn(t, ctrl, "work")
	resp, own := ctrl.StopAndDisconnect()
	if resp == nil || !own {
		t.Errorf("resp=%v own=%v", resp, own)
	}
}

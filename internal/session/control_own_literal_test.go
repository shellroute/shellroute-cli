package session

import (
	"strings"
	"testing"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/proxy/proxytest"
)

func TestConnectOwn_LiteralPasswordEntry(t *testing.T) {
	t.Setenv("SHELLROUTE_HOME", t.TempDir())
	p := proxytest.StartHTTPProxy(t, "u", "p@ss/w#rd?100% x")
	cfg := &config.Config{}
	cfg.AddUpstream(config.Upstream{Name: "work", URL: "http://u:p@ss/w#rd?100% x@" + p.Addr, Literal: true})
	ctrl := NewController(api.New("http://127.0.0.1:1", ""), cfg)
	withExitIPEndpoints(t, echoIP(t, `{"ip": "203.0.113.4", "country": "DE"}`).URL)
	t.Cleanup(func() { ctrl.StopAndDisconnect() })

	code, body := connectOwn(t, ctrl, "work")
	if code != 200 {
		t.Fatalf("code %d: %s", code, body)
	}
	if p.Requests.Load() == 0 || p.Rejected.Load() != 0 {
		t.Errorf("proxy saw %d authenticated and %d rejected requests", p.Requests.Load(), p.Rejected.Load())
	}
	if strings.Contains(body, "w#rd") {
		t.Errorf("exports contain the password:\n%s", body)
	}
}

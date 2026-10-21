//go:build !windows

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/session"
)

func cfgWithUpstream() *config.Config {
	cfg := config.Default()
	cfg.AddUpstream(config.Upstream{Name: "work", URL: "http://u:p@proxy.example:8080"})
	cfg.AddUpstream(config.Upstream{Name: "broken", URL: "ftp://x:1"})
	return cfg
}

func TestResolveRunTarget(t *testing.T) {
	cfg := cfgWithUpstream()
	withDefault := cfgWithUpstream()
	withDefault.DefaultCountry = "US"

	cases := []struct {
		name        string
		cfg         *config.Config
		args        []string
		dash        int
		proxy       string
		country     string
		env         string
		wantCountry string
		wantOwn     bool
		wantName    string
		wantRest    []string
		wantErr     string
	}{
		{name: "positional country", cfg: cfg, args: []string{"US", "curl", "x"}, dash: 1, wantCountry: "US", wantRest: []string{"curl", "x"}},
		{name: "positional country without dash", cfg: cfg, args: []string{"US", "curl"}, dash: -1, wantCountry: "US", wantRest: []string{"curl"}},
		{name: "country flag", cfg: cfg, args: []string{"curl"}, dash: 0, country: "DE", wantCountry: "DE", wantRest: []string{"curl"}},
		{name: "saved name", cfg: cfg, args: []string{"work", "curl"}, dash: 1, wantOwn: true, wantName: "work", wantRest: []string{"curl"}},
		{name: "saved name without dash", cfg: cfg, args: []string{"work", "curl"}, dash: -1, wantOwn: true, wantName: "work", wantRest: []string{"curl"}},
		{name: "unknown word without dash is the command", cfg: cfg, args: []string{"curl", "x"}, dash: -1, wantErr: "country required"},
		{name: "unknown name before dash", cfg: cfg, args: []string{"nope", "curl"}, dash: 1, wantErr: "unknown upstream \"nope\""},
		{name: "saved name with invalid URL", cfg: cfg, args: []string{"broken", "curl"}, dash: 1, wantErr: "invalid URL"},
		{name: "--proxy", cfg: cfg, args: []string{"curl"}, dash: 0, proxy: "socks5://h:1080", wantOwn: true, wantRest: []string{"curl"}},
		{name: "--proxy beats a saved name conflict", cfg: cfg, args: []string{"work", "curl"}, dash: 1, proxy: "socks5://h:1080", wantErr: "cannot be combined"},
		{name: "--proxy invalid", cfg: cfg, args: []string{"curl"}, dash: 0, proxy: "h:1080", wantErr: "--proxy"},
		{name: "--country with positional conflict", cfg: cfg, args: []string{"US", "curl"}, dash: 1, country: "DE", wantErr: "cannot be combined"},
		{name: "env proxy when nothing given", cfg: cfg, args: []string{"curl"}, dash: 0, env: "http://h:8080", wantOwn: true, wantRest: []string{"curl"}},
		{name: "explicit country beats env", cfg: cfg, args: []string{"US", "curl"}, dash: 1, env: "http://h:8080", wantCountry: "US", wantRest: []string{"curl"}},
		{name: "env invalid", cfg: cfg, args: []string{"curl"}, dash: 0, env: "nonsense", wantErr: "SHELLROUTE_PROXY"},
		{name: "default country", cfg: withDefault, args: []string{"curl"}, dash: 0, wantCountry: "US", wantRest: []string{"curl"}},
		{name: "nothing at all", cfg: cfg, args: []string{"curl"}, dash: 0, wantErr: "country required"},
		{name: "two words before dash", cfg: cfg, args: []string{"US", "DE", "curl"}, dash: 2, wantErr: "one country or upstream name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, rest, err := resolveRunTarget(tc.cfg, tc.args, tc.dash, tc.proxy, tc.country, tc.env)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.own() != tc.wantOwn || got.country != tc.wantCountry || got.name != tc.wantName {
				t.Errorf("target = own:%v country:%q name:%q", got.own(), got.country, got.name)
			}
			if strings.Join(rest, " ") != strings.Join(tc.wantRest, " ") {
				t.Errorf("rest = %v, want %v", rest, tc.wantRest)
			}
		})
	}
}

func TestResolveUpstreamRef(t *testing.T) {
	cfg := cfgWithUpstream()
	if got, err := resolveUpstreamRef(cfg, "work"); err != nil || got.name != "work" {
		t.Errorf("saved name: %+v %v", got, err)
	}
	if got, err := resolveUpstreamRef(cfg, "https://h:443"); err != nil || got.name != "" || !got.own() {
		t.Errorf("url: %+v %v", got, err)
	}
	if _, err := resolveUpstreamRef(cfg, "nope"); err == nil || !strings.Contains(err.Error(), "unknown upstream") {
		t.Errorf("unknown: %v", err)
	}
	if _, err := resolveUpstreamRef(cfg, ""); err == nil {
		t.Error("empty ref should fail")
	}
}

func TestOwnRouteSelected(t *testing.T) {
	withOwnConfig(t)
	// ownRouteSelected runs after cobra parsed the flags, so parse for real:
	// pflag records where "--" was.
	parseRun := func(argv ...string) []string {
		resetFlags(runCmd)
		if err := runCmd.ParseFlags(argv); err != nil {
			t.Fatal(err)
		}
		return runCmd.Flags().Args()
	}
	if ownRouteSelected(runCmd, parseRun("US", "--", "echo")) {
		t.Error("hosted country must keep the version check")
	}
	if !ownRouteSelected(runCmd, parseRun("--proxy", "http://h:8080", "--", "echo")) {
		t.Error("--proxy selects an own route")
	}
	t.Setenv(envProxyVar, "http://h:8080")
	if !ownRouteSelected(runCmd, parseRun("--", "echo")) {
		t.Error("SHELLROUTE_PROXY selects an own route")
	}
	if ownRouteSelected(runCmd, parseRun("US", "--", "echo")) {
		t.Error("an explicit country beats SHELLROUTE_PROXY")
	}
	t.Setenv(envProxyVar, "")
	resetFlags(runCmd)

	connectUpstream = "work"
	if !ownRouteSelected(connectCmd, nil) {
		t.Error("proxy --upstream selects an own route")
	}
	connectUpstream = ""
	if ownRouteSelected(connectCmd, nil) || ownRouteSelected(statusCmd, nil) || ownRouteSelected(proxyStopCmd, nil) {
		t.Error("hosted commands must keep the version check")
	}
	if !ownRouteSelected(upstreamCmd, nil) || !ownRouteSelected(upstreamAddCmd, nil) {
		t.Error("upstream commands need no account")
	}
}

func TestViaLine(t *testing.T) {
	s := &session.Session{Own: true, UpstreamName: "work", Country: "DE", ConnectMs: 212}
	s.SetExitIP("91.198.0.1")
	if got := viaLine(s); got != "via work · exit IP 91.198.0.1 · DE (observed) · 212 ms" {
		t.Errorf("viaLine = %q", got)
	}
	s.Country = ""
	if got := viaLine(s); got != "via work · exit IP 91.198.0.1 · 212 ms" {
		t.Errorf("viaLine without country = %q", got)
	}
	s2 := &session.Session{Own: true, Via: "socks5://h:1080"}
	if got := viaLine(s2); got != "via socks5://h:1080" {
		t.Errorf("viaLine without IP = %q", got)
	}
}

func TestRunOwn_ChildDoesNotSeeProxyURL(t *testing.T) {
	res := runHelper(t, false,
		"SR_TEST_HELPER=run-own-env",
		"SHELLROUTE_HOME="+t.TempDir(),
		"SHELLROUTE_API_KEY=",
		"SHELLROUTE_API_URL=http://127.0.0.1:1",
		"SHELLROUTE_PROXY=http://user:topsecret@127.0.0.1:1",
	)
	if res.sig != 0 {
		t.Fatalf("killed by %v: %s", res.sig, res.out)
	}
	if res.code != 0 {
		t.Errorf("the command saw SHELLROUTE_PROXY (exit %d)\n%s", res.code, res.out)
	}
	if strings.Contains(res.out, "topsecret") {
		t.Errorf("own-proxy password in the output:\n%s", res.out)
	}
}

func TestRunOwn_ExitCodeMatchesChild_NoAccount(t *testing.T) {
	dir := t.TempDir()
	env := []string{
		"SR_TEST_HELPER=run-own",
		"SHELLROUTE_HOME=" + dir,
		"SHELLROUTE_API_KEY=",
		"SHELLROUTE_PROXY=",
		"SHELLROUTE_API_URL=http://127.0.0.1:1",
	}
	for _, code := range []string{"3", "0"} {
		start := time.Now()
		res := runHelper(t, false, append(env, "SR_TEST_CHILD_EXIT="+code)...)
		if res.sig != 0 {
			t.Fatalf("exit %s: killed by %v: %s", code, res.sig, res.out)
		}
		want := 3
		if code == "0" {
			want = 0
		}
		if res.code != want {
			t.Errorf("exit %s: shellroute exited %d, want %d\n%s", code, res.code, want, res.out)
		}
		if strings.Contains(res.out, "Not authenticated") {
			t.Errorf("own route must not require an account:\n%s", res.out)
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("took %v: own route must not wait for the API", d)
		}
	}
}

func TestRun_UnknownUpstreamNameFailsBeforeAnySession(t *testing.T) {
	withOwnConfig(t)
	_, err := execCLI(t, "run", "nope", "--", "echo")
	if err == nil || !strings.Contains(err.Error(), "unknown upstream") {
		t.Errorf("err = %v", err)
	}
}

func TestRun_ProxyFlagConflict(t *testing.T) {
	withOwnConfig(t)
	_, err := execCLI(t, "run", "--proxy", "http://h:8080", "US", "--", "echo")
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("err = %v", err)
	}
}

func TestRun_EnvProxyIsUsed(t *testing.T) {
	withOwnConfig(t)
	t.Setenv(envProxyVar, "http://127.0.0.1:1") // nothing listening: the check fails fast
	withExitIPEndpoints(t, "http://127.0.0.1:1/ip")
	_, err := execCLI(t, "run", "--", "sh", "-c", "exit 0")
	if err == nil || err.Error() != "upstream unreachable" {
		t.Errorf("err = %v, want upstream unreachable (proves the env proxy was tried)", err)
	}
}

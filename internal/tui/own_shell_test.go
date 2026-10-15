package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOwnShellFile writes the shell functions an own-route session uses.
func writeOwnShellFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "shell.sh")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCleanupHelper(f)
	writeConnectFunc(f)
	writeConnectUpstreamFunc(f)
	writeRotateFunc(f)
	writeSimpleCommands(f)
	writeSettingsCommands(f)
	f.Close()
	return path
}

// fakeCurl records every URL and answers like the control port does for an
// own route. Extra lines are appended verbatim.
func fakeCurl(log string, extra string) string {
	return `curl() { for a in "$@"; do case "$a" in http://*) echo "$a" >> "` + log + `";; esac; done
  printf 'export SHELLROUTE_UPSTREAM=work\nexport SHELLROUTE_COUNTRY=DE\nexport SHELLROUTE_EXIT_IP=1.2.3.4\nexport SHELLROUTE_SESSION_ID=own-work\nexport SHELLROUTE_PORT=1\n` + extra + `'; }
`
}

func readLog(t *testing.T, path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

func TestConnectName_UsesUpstreamEndpoint(t *testing.T) {
	sh := writeOwnShellFile(t)
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "curl.log")
			out := execShell(t, shell, `source `+sh+`
				`+fakeCurl(log, "")+`
				export SHELLROUTE_CTRL=1
				/connect work
				echo "U=$SHELLROUTE_UPSTREAM C=$SHELLROUTE_COUNTRY IP=$SHELLROUTE_EXIT_IP"`)
			if !strings.Contains(out, "Connected through work — exit IP: 1.2.3.4 (DE, observed)") {
				t.Errorf("output:\n%s", out)
			}
			if !strings.Contains(out, "U=work C=DE IP=1.2.3.4") {
				t.Errorf("exports not applied:\n%s", out)
			}
			if !strings.Contains(readLog(t, log), "/connect?upstream=work") {
				t.Errorf("curl log: %q", readLog(t, log))
			}
		})
	}
}

func TestConnectCountry_StillUsesCountryEndpoint(t *testing.T) {
	sh := writeOwnShellFile(t)
	log := filepath.Join(t.TempDir(), "curl.log")
	execShell(t, "bash", `source `+sh+`
		`+fakeCurl(log, "")+`
		export SHELLROUTE_CTRL=1
		/connect us >/dev/null; true`)
	if !strings.Contains(readLog(t, log), "/connect?country=US") {
		t.Errorf("curl log: %q", readLog(t, log))
	}
}

func TestConnectName_RejectsOddCharactersWithoutCalling(t *testing.T) {
	sh := writeOwnShellFile(t)
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "curl.log")
			out := execShell(t, shell, `source `+sh+`
				`+fakeCurl(log, "")+`
				export SHELLROUTE_CTRL=1
				/connect "bad name"; true`)
			if !strings.Contains(out, "Unknown upstream") {
				t.Errorf("output:\n%s", out)
			}
			if readLog(t, log) != "" {
				t.Error("no control call should be made for an invalid name")
			}
		})
	}
}

func TestConnectName_ShowsProbeWarningWithoutEvaluatingIt(t *testing.T) {
	sh := writeOwnShellFile(t)
	log := filepath.Join(t.TempDir(), "curl.log")
	out := execShell(t, "bash", `source `+sh+`
		`+fakeCurl(log, `PROBE_WARNING=unexpected reply from the echo service\n`)+`
		export SHELLROUTE_CTRL=1
		/connect work
		echo "PW=${PROBE_WARNING:-none}"`)
	if !strings.Contains(out, "Exit IP unknown: unexpected reply from the echo service") {
		t.Errorf("warning not shown:\n%s", out)
	}
	if !strings.Contains(out, "PW=none") {
		t.Errorf("PROBE_WARNING must not be evaluated into the shell:\n%s", out)
	}
}

func TestRotate_OwnRouteDoesNothing(t *testing.T) {
	sh := writeOwnShellFile(t)
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "curl.log")
			out := execShell(t, shell, `source `+sh+`
				`+fakeCurl(log, "")+`
				export SHELLROUTE_CTRL=1 SHELLROUTE_SESSION_ID=own-work SHELLROUTE_UPSTREAM=work
				/rotate`)
			if !strings.Contains(out, "Rotate is only for Shellroute routes; your provider controls rotation.") {
				t.Errorf("output:\n%s", out)
			}
			if readLog(t, log) != "" {
				t.Error("/rotate must not call the control port on an own route")
			}
		})
	}
}

func TestAccountCommands_OwnRouteNotApplicable(t *testing.T) {
	sh := writeOwnShellFile(t)
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "curl.log")
			out := execShell(t, shell, `source `+sh+`
				`+fakeCurl(log, "")+`
				export SHELLROUTE_CTRL=1 SHELLROUTE_SESSION_ID=own-work SHELLROUTE_UPSTREAM=work
				/balance; /countries; /cities US; /iptype datacenter; /sticky on
				echo "T=${SHELLROUTE_IPTYPE:-unset} S=${SHELLROUTE_STICKY:-unset}"`)
			if strings.Count(out, "Not applicable to your own upstream.") != 5 {
				t.Errorf("want five not-applicable lines:\n%s", out)
			}
			if !strings.Contains(out, "T=unset S=unset") {
				t.Errorf("settings must not change on an own route:\n%s", out)
			}
			if readLog(t, log) != "" {
				t.Error("no control calls expected")
			}
		})
	}
}

func TestAccountCommands_HostedRouteStillCall(t *testing.T) {
	sh := writeOwnShellFile(t)
	log := filepath.Join(t.TempDir(), "curl.log")
	execShell(t, "bash", `source `+sh+`
		`+fakeCurl(log, "")+`
		export SHELLROUTE_CTRL=1 SHELLROUTE_SESSION_ID=sess SHELLROUTE_COUNTRY=US
		/balance >/dev/null; /countries >/dev/null; true`)
	got := readLog(t, log)
	if !strings.Contains(got, "/balance") || !strings.Contains(got, "/countries") {
		t.Errorf("hosted route should call the control port: %q", got)
	}
}

func TestCleanup_UnsetsUpstream(t *testing.T) {
	sh := writeOwnShellFile(t)
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `source `+sh+`
				export SHELLROUTE_UPSTREAM=work SHELLROUTE_SESSION_ID=own-work
				_sr_cleanup_session
				echo "${SHELLROUTE_UPSTREAM:-UNSET} ${SHELLROUTE_SESSION_ID:-UNSET}"`)
			if out != "UNSET UNSET" {
				t.Errorf("got %q", out)
			}
		})
	}
}

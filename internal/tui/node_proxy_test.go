package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for NODE_USE_ENV_PROXY ownership in interactive mode.
// Uses the actual writeCleanupHelper and writeConnectFunc output.

func writeShellFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "shell.sh")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Write actual production shell functions
	writeCleanupHelper(f)
	writeConnectFunc(f)
	writeDisconnectFunc(f)
	writeRotateFunc(f)
	f.Close()
	return path
}

func bash(t *testing.T, script string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash failed: %v\nscript: %s\noutput: %s", err, script, out)
	}
	return strings.TrimSpace(string(out))
}

func bashAllowFail(t *testing.T, script string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	out, _ := cmd.CombinedOutput()
	return strings.TrimSpace(string(out))
}

// --- Cleanup helper tests (production _sr_cleanup_session) ---

func TestCleanup_OwnsAndRemoves(t *testing.T) {
	sh := writeShellFile(t)
	out := bash(t, `source `+sh+`
		export NODE_USE_ENV_PROXY=1
		_SR_OWNS_NODE_PROXY=1
		_sr_cleanup_session
		echo "${NODE_USE_ENV_PROXY:-UNSET}"
	`)
	if out != "UNSET" {
		t.Errorf("NODE_USE_ENV_PROXY=%q, want UNSET", out)
	}
}

func TestCleanup_PreservesUserZero(t *testing.T) {
	sh := writeShellFile(t)
	out := bash(t, `source `+sh+`
		export NODE_USE_ENV_PROXY=0
		_sr_cleanup_session
		echo "$NODE_USE_ENV_PROXY"
	`)
	if out != "0" {
		t.Errorf("NODE_USE_ENV_PROXY=%q, want 0 (user preserved)", out)
	}
}

func TestCleanup_PreservesUserOne(t *testing.T) {
	sh := writeShellFile(t)
	// User set 1 before connect — no ownership marker
	out := bash(t, `source `+sh+`
		export NODE_USE_ENV_PROXY=1
		_sr_cleanup_session
		echo "$NODE_USE_ENV_PROXY"
	`)
	if out != "1" {
		t.Errorf("NODE_USE_ENV_PROXY=%q, want 1 (user preserved)", out)
	}
}

func TestCleanup_UserChangedOwnedToZero(t *testing.T) {
	sh := writeShellFile(t)
	out := bash(t, `source `+sh+`
		_SR_OWNS_NODE_PROXY=1
		export NODE_USE_ENV_PROXY=0
		_sr_cleanup_session
		echo "$NODE_USE_ENV_PROXY"
	`)
	if out != "0" {
		t.Errorf("NODE_USE_ENV_PROXY=%q, want 0 (user override survives)", out)
	}
}

func TestCleanup_MarkerAlwaysCleared(t *testing.T) {
	sh := writeShellFile(t)
	out := bash(t, `source `+sh+`
		_SR_OWNS_NODE_PROXY=1
		export NODE_USE_ENV_PROXY=1
		_sr_cleanup_session
		echo "${_SR_OWNS_NODE_PROXY:-UNSET}"
	`)
	if out != "UNSET" {
		t.Errorf("_SR_OWNS_NODE_PROXY=%q, want UNSET", out)
	}
}

func TestCleanup_ProxyVarsCleared(t *testing.T) {
	sh := writeShellFile(t)
	out := bash(t, `source `+sh+`
		export HTTP_PROXY=http://127.0.0.1:41900
		export SHELLROUTE_SESSION_ID=test
		_sr_cleanup_session
		echo "HTTP_PROXY=${HTTP_PROXY:-UNSET} SESSION=${SHELLROUTE_SESSION_ID:-UNSET}"
	`)
	if out != "HTTP_PROXY=UNSET SESSION=UNSET" {
		t.Errorf("cleanup: %q, want both UNSET", out)
	}
}

// --- All cleanup paths call _sr_cleanup_session ---

func TestDisconnect_CallsCleanup(t *testing.T) {
	sh := writeShellFile(t)
	out := bash(t, `source `+sh+`; type /disconnect`)
	if !strings.Contains(out, "_sr_cleanup_session") {
		t.Error("/disconnect does not call _sr_cleanup_session")
	}
}

func TestConnectDisconnected_CallsCleanup(t *testing.T) {
	sh := writeShellFile(t)
	out := bash(t, `source `+sh+`; type /connect`)
	if !strings.Contains(out, "_sr_cleanup_session") {
		t.Error("/connect DISCONNECTED path does not call _sr_cleanup_session")
	}
}

func TestRotateDisconnected_CallsCleanup(t *testing.T) {
	sh := writeShellFile(t)
	out := bash(t, `source `+sh+`; type /rotate`)
	if !strings.Contains(out, "_sr_cleanup_session") {
		t.Error("/rotate DISCONNECTED path does not call _sr_cleanup_session")
	}
}

// --- Test emitted controller script (NO_PROXY union + NODE_USE_ENV_PROXY) ---

func TestControllerScript_NoProxyUnion(t *testing.T) {
	// Simulate the controller output that /connect evals
	out := bash(t, `
		export NO_PROXY=corp.internal
		unset no_proxy
		# Controller script (from control.go)
		_sr_union_no_proxy() {
		  local IFS=,; local seen="" result=""
		  for src in "$NO_PROXY" "$no_proxy"; do
		    for h in $src; do
		      h=$(echo "$h" | xargs)
		      [ -z "$h" ] && continue
		      echo ",$seen," | grep -qF ",$h," && continue
		      seen="$seen,$h"; result="${result:+$result,}$h"
		    done
		  done
		  for h in localhost 127.0.0.1 ::1; do
		    echo ",$seen," | grep -qF ",$h," && continue
		    result="${result:+$result,}$h"
		  done
		  echo "$result"
		}
		_sr_np=$(_sr_union_no_proxy); export NO_PROXY="$_sr_np"; export no_proxy="$_sr_np"; unset _sr_np
		echo "NO_PROXY=$NO_PROXY no_proxy=$no_proxy"
	`)
	for _, host := range []string{"corp.internal", "localhost", "127.0.0.1", "::1"} {
		if !strings.Contains(out, host) {
			t.Errorf("output %q missing %s", out, host)
		}
	}
	// Both vars should contain the same value
	parts := strings.SplitN(out, " ", 2)
	if len(parts) == 2 {
		upper := strings.TrimPrefix(parts[0], "NO_PROXY=")
		lower := strings.TrimPrefix(parts[1], "no_proxy=")
		if upper != lower {
			t.Errorf("NO_PROXY=%q != no_proxy=%q", upper, lower)
		}
	}
}

func TestControllerScript_NodeProxyOwnership(t *testing.T) {
	// Test the controller's conditional from control.go
	script := `if [ -z "$NODE_USE_ENV_PROXY" ]; then export NODE_USE_ENV_PROXY=1; _SR_OWNS_NODE_PROXY=1; fi`

	// Absent → sets with marker
	out := bash(t, `unset NODE_USE_ENV_PROXY; `+script+`; echo "val=$NODE_USE_ENV_PROXY marker=$_SR_OWNS_NODE_PROXY"`)
	if out != "val=1 marker=1" {
		t.Errorf("absent: %q, want val=1 marker=1", out)
	}

	// Preset 0 → preserved, no marker
	out = bash(t, `export NODE_USE_ENV_PROXY=0; `+script+`; echo "val=$NODE_USE_ENV_PROXY marker=${_SR_OWNS_NODE_PROXY:-none}"`)
	if out != "val=0 marker=none" {
		t.Errorf("preset 0: %q, want val=0 marker=none", out)
	}

	// Preset 1 → preserved, no marker
	out = bash(t, `export NODE_USE_ENV_PROXY=1; `+script+`; echo "val=$NODE_USE_ENV_PROXY marker=${_SR_OWNS_NODE_PROXY:-none}"`)
	if out != "val=1 marker=none" {
		t.Errorf("preset 1: %q, want val=1 marker=none", out)
	}
}

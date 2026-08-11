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
	// Write the actual production cleanup helper
	writeCleanupHelper(f)
	// Write a minimal connect simulator that evals the controller output
	writeConnectFunc(f)
	writeDisconnectFunc(f)
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

// --- Disconnect calls cleanup ---

func TestDisconnect_CallsCleanup(t *testing.T) {
	sh := writeShellFile(t)
	// /disconnect calls _sr_cleanup_session. Since we can't call the real
	// controller, test that the disconnect function body contains the call.
	out := bash(t, `source `+sh+`; type /disconnect`)
	if !strings.Contains(out, "_sr_cleanup_session") {
		t.Error("/disconnect does not call _sr_cleanup_session")
	}
}

// --- DISCONNECTED path in /connect calls cleanup ---

func TestConnectDisconnected_CallsCleanup(t *testing.T) {
	sh := writeShellFile(t)
	out := bash(t, `source `+sh+`; type /connect`)
	if !strings.Contains(out, "_sr_cleanup_session") {
		t.Error("/connect DISCONNECTED path does not call _sr_cleanup_session")
	}
}

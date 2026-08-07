package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for NODE_USE_ENV_PROXY ownership in interactive mode shell functions.
// We write the shell functions to a temp file and eval them in bash.

func writeTempShell(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "shell.sh")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCleanupHelper(f)
	f.Close()
	return path
}

func runBash(t *testing.T, script string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\noutput: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCleanup_OwnsAndRemoves(t *testing.T) {
	sh := writeTempShell(t)
	// Shellroute owns NODE_USE_ENV_PROXY (set marker + value=1)
	out := runBash(t, `
		source `+sh+`
		export NODE_USE_ENV_PROXY=1
		_SR_OWNS_NODE_PROXY=1
		_sr_cleanup_session
		echo "${NODE_USE_ENV_PROXY:-unset}"
	`)
	if out != "unset" {
		t.Errorf("after cleanup, NODE_USE_ENV_PROXY=%q, want unset", out)
	}
}

func TestCleanup_PreservesUserZero(t *testing.T) {
	sh := writeTempShell(t)
	// User set NODE_USE_ENV_PROXY=0 before connect — no marker
	out := runBash(t, `
		source `+sh+`
		export NODE_USE_ENV_PROXY=0
		_sr_cleanup_session
		echo "$NODE_USE_ENV_PROXY"
	`)
	if out != "0" {
		t.Errorf("after cleanup, NODE_USE_ENV_PROXY=%q, want 0 (user value preserved)", out)
	}
}

func TestCleanup_PreservesUserOne(t *testing.T) {
	sh := writeTempShell(t)
	// User set NODE_USE_ENV_PROXY=1 before connect — no marker
	out := runBash(t, `
		source `+sh+`
		export NODE_USE_ENV_PROXY=1
		_sr_cleanup_session
		echo "$NODE_USE_ENV_PROXY"
	`)
	if out != "1" {
		t.Errorf("after cleanup, NODE_USE_ENV_PROXY=%q, want 1 (user value preserved)", out)
	}
}

func TestCleanup_UserChangedOwnedValue(t *testing.T) {
	sh := writeTempShell(t)
	// Shellroute set 1 with marker, then user changed to 0
	out := runBash(t, `
		source `+sh+`
		export NODE_USE_ENV_PROXY=0
		_SR_OWNS_NODE_PROXY=1
		_sr_cleanup_session
		echo "$NODE_USE_ENV_PROXY"
	`)
	if out != "0" {
		t.Errorf("after cleanup, NODE_USE_ENV_PROXY=%q, want 0 (user override preserved)", out)
	}
}

func TestCleanup_MarkerCleared(t *testing.T) {
	sh := writeTempShell(t)
	out := runBash(t, `
		source `+sh+`
		export NODE_USE_ENV_PROXY=1
		_SR_OWNS_NODE_PROXY=1
		_sr_cleanup_session
		echo "${_SR_OWNS_NODE_PROXY:-unset}"
	`)
	if out != "unset" {
		t.Errorf("after cleanup, _SR_OWNS_NODE_PROXY=%q, want unset", out)
	}
}

func TestCleanup_ProxyVarsCleared(t *testing.T) {
	sh := writeTempShell(t)
	out := runBash(t, `
		source `+sh+`
		export HTTP_PROXY=http://127.0.0.1:41900
		export SHELLROUTE_SESSION_ID=test
		_sr_cleanup_session
		echo "HTTP_PROXY=${HTTP_PROXY:-unset} SESSION=${SHELLROUTE_SESSION_ID:-unset}"
	`)
	if out != "HTTP_PROXY=unset SESSION=unset" {
		t.Errorf("after cleanup: %q, want both unset", out)
	}
}

func TestConnectOutput_SetsMarker(t *testing.T) {
	// Test the connect output line from control.go
	script := `if [ -z "$NODE_USE_ENV_PROXY" ]; then export NODE_USE_ENV_PROXY=1; _SR_OWNS_NODE_PROXY=1; fi`
	out := runBash(t, `
		unset NODE_USE_ENV_PROXY
		`+script+`
		echo "val=$NODE_USE_ENV_PROXY marker=$_SR_OWNS_NODE_PROXY"
	`)
	if out != "val=1 marker=1" {
		t.Errorf("connect output: %q, want val=1 marker=1", out)
	}
}

func TestConnectOutput_SkipsWhenPreset(t *testing.T) {
	script := `if [ -z "$NODE_USE_ENV_PROXY" ]; then export NODE_USE_ENV_PROXY=1; _SR_OWNS_NODE_PROXY=1; fi`
	out := runBash(t, `
		export NODE_USE_ENV_PROXY=0
		`+script+`
		echo "val=$NODE_USE_ENV_PROXY marker=${_SR_OWNS_NODE_PROXY:-unset}"
	`)
	if out != "val=0 marker=unset" {
		t.Errorf("connect with preset: %q, want val=0 marker=unset", out)
	}
}

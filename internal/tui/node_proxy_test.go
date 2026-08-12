package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shellroute/shellroute-cli/internal/session"
)

func writeShellFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "shell.sh")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCleanupHelper(f)
	writeConnectFunc(f)
	writeDisconnectFunc(f)
	writeRotateFunc(f)
	f.Close()
	return path
}

// runShell runs a script under the given shell (bash or zsh).
func execShell(t *testing.T, shell, script string) string {
	t.Helper()
	cmd := exec.Command(shell, "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\nscript: %s\noutput: %s", shell, err, script, out)
	}
	return strings.TrimSpace(string(out))
}

func shells() []string {
	s := []string{"bash"}
	if _, err := exec.LookPath("zsh"); err == nil {
		s = append(s, "zsh")
	}
	return s
}

// --- Cleanup helper tests (production _sr_cleanup_session) ---

func TestCleanup_OwnsAndRemoves(t *testing.T) {
	sh := writeShellFile(t)
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `source `+sh+`
				export NODE_USE_ENV_PROXY=1; _SR_OWNS_NODE_PROXY=1
				_sr_cleanup_session
				echo "${NODE_USE_ENV_PROXY:-UNSET}"`)
			if out != "UNSET" {
				t.Errorf("NODE_USE_ENV_PROXY=%q, want UNSET", out)
			}
		})
	}
}

func TestCleanup_PreservesUserZero(t *testing.T) {
	sh := writeShellFile(t)
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `source `+sh+`
				export NODE_USE_ENV_PROXY=0
				_sr_cleanup_session
				echo "$NODE_USE_ENV_PROXY"`)
			if out != "0" {
				t.Errorf("NODE_USE_ENV_PROXY=%q, want 0", out)
			}
		})
	}
}

func TestCleanup_PreservesUserOne(t *testing.T) {
	sh := writeShellFile(t)
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `source `+sh+`
				export NODE_USE_ENV_PROXY=1
				_sr_cleanup_session
				echo "$NODE_USE_ENV_PROXY"`)
			if out != "1" {
				t.Errorf("NODE_USE_ENV_PROXY=%q, want 1", out)
			}
		})
	}
}

func TestCleanup_UserChangedOwnedToZero(t *testing.T) {
	sh := writeShellFile(t)
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `source `+sh+`
				_SR_OWNS_NODE_PROXY=1; export NODE_USE_ENV_PROXY=0
				_sr_cleanup_session
				echo "$NODE_USE_ENV_PROXY"`)
			if out != "0" {
				t.Errorf("NODE_USE_ENV_PROXY=%q, want 0 (user override survives)", out)
			}
		})
	}
}

func TestCleanup_MarkerAlwaysCleared(t *testing.T) {
	sh := writeShellFile(t)
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `source `+sh+`
				_SR_OWNS_NODE_PROXY=1; export NODE_USE_ENV_PROXY=1
				_sr_cleanup_session
				echo "${_SR_OWNS_NODE_PROXY:-UNSET}"`)
			if out != "UNSET" {
				t.Errorf("_SR_OWNS_NODE_PROXY=%q, want UNSET", out)
			}
		})
	}
}

// --- All cleanup paths call _sr_cleanup_session ---

func TestDisconnect_CallsCleanup(t *testing.T) {
	sh := writeShellFile(t)
	out := execShell(t, "bash", `source `+sh+`; type /disconnect`)
	if !strings.Contains(out, "_sr_cleanup_session") {
		t.Error("/disconnect does not call _sr_cleanup_session")
	}
}

func TestConnectDisconnected_CallsCleanup(t *testing.T) {
	sh := writeShellFile(t)
	out := execShell(t, "bash", `source `+sh+`; type /connect`)
	if !strings.Contains(out, "_sr_cleanup_session") {
		t.Error("/connect DISCONNECTED path does not call _sr_cleanup_session")
	}
}

func TestRotateDisconnected_CallsCleanup(t *testing.T) {
	sh := writeShellFile(t)
	out := execShell(t, "bash", `source `+sh+`; type /rotate`)
	if !strings.Contains(out, "_sr_cleanup_session") {
		t.Error("/rotate DISCONNECTED path does not call _sr_cleanup_session")
	}
}

// --- Test production NO_PROXY union script (from session.NoProxyUnionScript) ---

func TestNoProxyUnion_Star(t *testing.T) {
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `
				export NO_PROXY="*"
				unset no_proxy
				`+session.NoProxyUnionScript+`
				_sr_np=$(_sr_union_no_proxy)
				echo "$_sr_np"`)
			if !strings.HasPrefix(out, "*") {
				t.Errorf("NO_PROXY=* lost: got %q", out)
			}
		})
	}
}

func TestNoProxyUnion_WildcardDomain(t *testing.T) {
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `
				export NO_PROXY=".corp.internal,*.test.local"
				unset no_proxy
				`+session.NoProxyUnionScript+`
				_sr_np=$(_sr_union_no_proxy)
				echo "$_sr_np"`)
			if !strings.Contains(out, ".corp.internal") {
				t.Errorf("lost .corp.internal: %q", out)
			}
			if !strings.Contains(out, "*.test.local") {
				t.Errorf("lost *.test.local: %q", out)
			}
			if !strings.Contains(out, "127.0.0.1") {
				t.Errorf("missing loopback: %q", out)
			}
		})
	}
}

func TestNoProxyUnion_UpperOnly(t *testing.T) {
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `
				export NO_PROXY="corp.internal"
				unset no_proxy
				`+session.NoProxyUnionScript+`
				export NO_PROXY=$(_sr_union_no_proxy)
				export no_proxy="$NO_PROXY"
				echo "NO_PROXY=$NO_PROXY no_proxy=$no_proxy"`)
			if !strings.Contains(out, "corp.internal") {
				t.Errorf("lost corp.internal: %q", out)
			}
			parts := strings.SplitN(out, " ", 2)
			if len(parts) == 2 {
				upper := strings.TrimPrefix(parts[0], "NO_PROXY=")
				lower := strings.TrimPrefix(parts[1], "no_proxy=")
				if upper != lower {
					t.Errorf("NO_PROXY=%q != no_proxy=%q", upper, lower)
				}
			}
		})
	}
}

func TestNoProxyUnion_LowerOnly(t *testing.T) {
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `
				unset NO_PROXY
				export no_proxy="corp.internal"
				`+session.NoProxyUnionScript+`
				_sr_np=$(_sr_union_no_proxy)
				echo "$_sr_np"`)
			if !strings.Contains(out, "corp.internal") {
				t.Errorf("lost corp.internal: %q", out)
			}
		})
	}
}

func TestNoProxyUnion_DifferingLists(t *testing.T) {
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `
				export NO_PROXY="upper.host"
				export no_proxy="lower.host"
				`+session.NoProxyUnionScript+`
				_sr_np=$(_sr_union_no_proxy)
				echo "$_sr_np"`)
			for _, host := range []string{"upper.host", "lower.host", "localhost", "127.0.0.1", "::1"} {
				if !strings.Contains(out, host) {
					t.Errorf("missing %s in %q", host, out)
				}
			}
		})
	}
}

func TestNoProxyUnion_Deduplicates(t *testing.T) {
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `
				export NO_PROXY="localhost,myhost"
				export no_proxy="localhost,myhost"
				`+session.NoProxyUnionScript+`
				_sr_np=$(_sr_union_no_proxy)
				echo "$_sr_np"`)
			if strings.Count(out, "localhost") != 1 {
				t.Errorf("duplicate localhost: %q", out)
			}
		})
	}
}

// --- Test production NODE_USE_ENV_PROXY script (from session.NodeProxyOwnershipScript) ---

func TestNodeProxyOwnership_Absent(t *testing.T) {
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `
				unset NODE_USE_ENV_PROXY
				`+session.NodeProxyOwnershipScript+`
				echo "val=$NODE_USE_ENV_PROXY marker=$_SR_OWNS_NODE_PROXY"`)
			if out != "val=1 marker=1" {
				t.Errorf("absent: %q, want val=1 marker=1", out)
			}
		})
	}
}

func TestNodeProxyOwnership_PresetZero(t *testing.T) {
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `
				export NODE_USE_ENV_PROXY=0
				`+session.NodeProxyOwnershipScript+`
				echo "val=$NODE_USE_ENV_PROXY marker=${_SR_OWNS_NODE_PROXY:-none}"`)
			if out != "val=0 marker=none" {
				t.Errorf("preset 0: %q, want val=0 marker=none", out)
			}
		})
	}
}

func TestNodeProxyOwnership_PresetOne(t *testing.T) {
	for _, shell := range shells() {
		t.Run(shell, func(t *testing.T) {
			out := execShell(t, shell, `
				export NODE_USE_ENV_PROXY=1
				`+session.NodeProxyOwnershipScript+`
				echo "val=$NODE_USE_ENV_PROXY marker=${_SR_OWNS_NODE_PROXY:-none}"`)
			if out != "val=1 marker=none" {
				t.Errorf("preset 1: %q, want val=1 marker=none", out)
			}
		})
	}
}

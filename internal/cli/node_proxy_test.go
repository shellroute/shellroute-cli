package cli

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Tests for buildProxyEnv (production function in run.go).

func TestBuildProxyEnv_NodeProxyAbsent(t *testing.T) {
	base := filterEnv(os.Environ(), "NODE_USE_ENV_PROXY")
	env := buildProxyEnv(base, "http://127.0.0.1:41900")
	if v := envLookup(env, "NODE_USE_ENV_PROXY"); v != "1" {
		t.Errorf("NODE_USE_ENV_PROXY = %q, want 1", v)
	}
}

func TestBuildProxyEnv_NodeProxyZeroPreserved(t *testing.T) {
	base := setEnv(os.Environ(), "NODE_USE_ENV_PROXY", "0")
	env := buildProxyEnv(base, "http://127.0.0.1:41900")
	if v := envLookup(env, "NODE_USE_ENV_PROXY"); v != "0" {
		t.Errorf("NODE_USE_ENV_PROXY = %q, want 0 (user opt-out)", v)
	}
}

func TestBuildProxyEnv_NodeProxyOneNoDuplicate(t *testing.T) {
	base := setEnv(os.Environ(), "NODE_USE_ENV_PROXY", "1")
	env := buildProxyEnv(base, "http://127.0.0.1:41900")
	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, "NODE_USE_ENV_PROXY=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("NODE_USE_ENV_PROXY appears %d times, want 1", count)
	}
}

func TestBuildProxyEnv_NoProxyAdded(t *testing.T) {
	base := filterEnv(os.Environ(), "NO_PROXY", "no_proxy")
	env := buildProxyEnv(base, "http://127.0.0.1:41900")
	np := envLookup(env, "NO_PROXY")
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		if !strings.Contains(np, host) {
			t.Errorf("NO_PROXY=%q missing %s", np, host)
		}
	}
}

func TestBuildProxyEnv_NoProxyPreservesUser(t *testing.T) {
	base := setEnv(os.Environ(), "NO_PROXY", "myhost.local")
	env := buildProxyEnv(base, "http://127.0.0.1:41900")
	np := envLookup(env, "NO_PROXY")
	if !strings.Contains(np, "myhost.local") {
		t.Errorf("NO_PROXY=%q should contain user entry myhost.local", np)
	}
	if !strings.Contains(np, "127.0.0.1") {
		t.Errorf("NO_PROXY=%q should contain 127.0.0.1", np)
	}
}

func TestBuildProxyEnv_ProxyVarsSet(t *testing.T) {
	env := buildProxyEnv(os.Environ(), "http://127.0.0.1:41900")
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if v := envLookup(env, key); v != "http://127.0.0.1:41900" {
			t.Errorf("%s = %q, want proxy URL", key, v)
		}
	}
}

// Verify child process actually receives the vars
func TestBuildProxyEnv_ChildReceivesNodeProxy(t *testing.T) {
	base := filterEnv(os.Environ(), "NODE_USE_ENV_PROXY")
	cmd := exec.Command("sh", "-c", "echo $NODE_USE_ENV_PROXY")
	cmd.Env = buildProxyEnv(base, "http://127.0.0.1:41900")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	if v := strings.TrimSpace(string(out)); v != "1" {
		t.Errorf("child saw NODE_USE_ENV_PROXY=%q, want 1", v)
	}
}

func TestBuildProxyEnv_ChildSeesZero(t *testing.T) {
	base := setEnv(os.Environ(), "NODE_USE_ENV_PROXY", "0")
	cmd := exec.Command("sh", "-c", "echo $NODE_USE_ENV_PROXY")
	cmd.Env = buildProxyEnv(base, "http://127.0.0.1:41900")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	if v := strings.TrimSpace(string(out)); v != "0" {
		t.Errorf("child saw NODE_USE_ENV_PROXY=%q, want 0", v)
	}
}

func TestMergeNoProxy_Empty(t *testing.T) {
	got := mergeNoProxy("")
	if got != defaultNoProxy {
		t.Errorf("mergeNoProxy('') = %q, want %q", got, defaultNoProxy)
	}
}

func TestMergeNoProxy_AlreadyComplete(t *testing.T) {
	got := mergeNoProxy("localhost,127.0.0.1,::1")
	if got != "localhost,127.0.0.1,::1" {
		t.Errorf("mergeNoProxy = %q, should not add duplicates", got)
	}
}

func TestMergeNoProxy_MergesUserEntries(t *testing.T) {
	got := mergeNoProxy("myapp.local")
	if !strings.Contains(got, "myapp.local") {
		t.Errorf("lost user entry: %q", got)
	}
	for _, h := range []string{"localhost", "127.0.0.1", "::1"} {
		if !strings.Contains(got, h) {
			t.Errorf("missing %s in %q", h, got)
		}
	}
}

// helpers

func filterEnv(env []string, keys ...string) []string {
	var out []string
	for _, e := range env {
		skip := false
		for _, k := range keys {
			if strings.HasPrefix(e, k+"=") {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, e)
		}
	}
	return out
}

func setEnv(env []string, key, val string) []string {
	return append(filterEnv(env, key), key+"="+val)
}

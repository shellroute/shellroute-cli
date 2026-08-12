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

func TestUnionNoProxy_BothEmpty(t *testing.T) {
	got := unionNoProxy("", "")
	if got != defaultNoProxy {
		t.Errorf("unionNoProxy('','') = %q, want %q", got, defaultNoProxy)
	}
}

func TestUnionNoProxy_AlreadyComplete(t *testing.T) {
	got := unionNoProxy("localhost,127.0.0.1,::1", "")
	for _, h := range []string{"localhost", "127.0.0.1", "::1"} {
		if !strings.Contains(got, h) {
			t.Errorf("missing %s in %q", h, got)
		}
	}
	// No duplicates
	if strings.Count(got, "localhost") != 1 {
		t.Errorf("duplicate localhost in %q", got)
	}
}

func TestUnionNoProxy_UppercaseOnly(t *testing.T) {
	// User only set NO_PROXY (uppercase), no_proxy is empty
	base := setEnv(filterEnv(os.Environ(), "NO_PROXY", "no_proxy"), "NO_PROXY", "corp.internal")
	env := buildProxyEnv(base, "http://127.0.0.1:41900")
	np := envLookup(env, "no_proxy")
	if !strings.Contains(np, "corp.internal") {
		t.Errorf("no_proxy=%q missing corp.internal from NO_PROXY", np)
	}
	if !strings.Contains(np, "127.0.0.1") {
		t.Errorf("no_proxy=%q missing loopback", np)
	}
}

func TestUnionNoProxy_LowercaseOnly(t *testing.T) {
	// User only set no_proxy (lowercase), NO_PROXY is empty
	base := setEnv(filterEnv(os.Environ(), "NO_PROXY", "no_proxy"), "no_proxy", "corp.internal")
	env := buildProxyEnv(base, "http://127.0.0.1:41900")
	np := envLookup(env, "NO_PROXY")
	if !strings.Contains(np, "corp.internal") {
		t.Errorf("NO_PROXY=%q missing corp.internal from no_proxy", np)
	}
	if !strings.Contains(np, "127.0.0.1") {
		t.Errorf("NO_PROXY=%q missing loopback", np)
	}
}

func TestUnionNoProxy_BothSet(t *testing.T) {
	// Both set with different entries
	base := setEnv(
		setEnv(filterEnv(os.Environ(), "NO_PROXY", "no_proxy"), "NO_PROXY", "upper.host"),
		"no_proxy", "lower.host",
	)
	env := buildProxyEnv(base, "http://127.0.0.1:41900")
	np := envLookup(env, "NO_PROXY")
	for _, host := range []string{"upper.host", "lower.host", "localhost", "127.0.0.1", "::1"} {
		if !strings.Contains(np, host) {
			t.Errorf("NO_PROXY=%q missing %s", np, host)
		}
	}
	// Both vars should be identical
	if envLookup(env, "NO_PROXY") != envLookup(env, "no_proxy") {
		t.Error("NO_PROXY and no_proxy should be identical")
	}
}

func TestUnionNoProxy_Deduplicates(t *testing.T) {
	got := unionNoProxy("localhost,myhost", "localhost,myhost")
	if strings.Count(got, "localhost") != 1 {
		t.Errorf("duplicate localhost in %q", got)
	}
	if strings.Count(got, "myhost") != 1 {
		t.Errorf("duplicate myhost in %q", got)
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

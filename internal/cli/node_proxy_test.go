package cli

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Tests for NODE_USE_ENV_PROXY injection in direct mode (run.go).
// Direct mode builds childCmd.Env from os.Environ() + proxy vars.
// We test the env-building logic by simulating what run.go does.

func buildDirectEnv(proxyURL string) []string {
	env := append(os.Environ(),
		"HTTP_PROXY="+proxyURL,
		"HTTPS_PROXY="+proxyURL,
		"http_proxy="+proxyURL,
		"https_proxy="+proxyURL,
	)
	if os.Getenv("NODE_USE_ENV_PROXY") == "" {
		env = append(env, "NODE_USE_ENV_PROXY=1")
	}
	return env
}

func envValue(env []string, key string) string {
	prefix := key + "="
	// Last occurrence wins (same as exec.Command behavior)
	val := ""
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			val = e[len(prefix):]
		}
	}
	return val
}

func TestNodeProxyDirect_AbsentSetsOne(t *testing.T) {
	t.Setenv("NODE_USE_ENV_PROXY", "")
	os.Unsetenv("NODE_USE_ENV_PROXY")
	env := buildDirectEnv("http://127.0.0.1:41900")
	if v := envValue(env, "NODE_USE_ENV_PROXY"); v != "1" {
		t.Errorf("NODE_USE_ENV_PROXY = %q, want 1", v)
	}
}

func TestNodeProxyDirect_ZeroPreserved(t *testing.T) {
	t.Setenv("NODE_USE_ENV_PROXY", "0")
	env := buildDirectEnv("http://127.0.0.1:41900")
	if v := envValue(env, "NODE_USE_ENV_PROXY"); v != "0" {
		t.Errorf("NODE_USE_ENV_PROXY = %q, want 0 (user opt-out preserved)", v)
	}
}

func TestNodeProxyDirect_OnePreserved(t *testing.T) {
	t.Setenv("NODE_USE_ENV_PROXY", "1")
	env := buildDirectEnv("http://127.0.0.1:41900")
	// Should not duplicate — value stays 1
	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, "NODE_USE_ENV_PROXY=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("NODE_USE_ENV_PROXY appears %d times, want 1 (no duplicate)", count)
	}
	if v := envValue(env, "NODE_USE_ENV_PROXY"); v != "1" {
		t.Errorf("NODE_USE_ENV_PROXY = %q, want 1", v)
	}
}

// Verify the child actually receives the env var via a real exec
func TestNodeProxyDirect_ChildReceivesVar(t *testing.T) {
	t.Setenv("NODE_USE_ENV_PROXY", "")
	os.Unsetenv("NODE_USE_ENV_PROXY")
	cmd := exec.Command("sh", "-c", "echo $NODE_USE_ENV_PROXY")
	cmd.Env = buildDirectEnv("http://127.0.0.1:41900")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	if v := strings.TrimSpace(string(out)); v != "1" {
		t.Errorf("child saw NODE_USE_ENV_PROXY=%q, want 1", v)
	}
}

func TestNodeProxyDirect_ChildSeesZero(t *testing.T) {
	t.Setenv("NODE_USE_ENV_PROXY", "0")
	cmd := exec.Command("sh", "-c", "echo $NODE_USE_ENV_PROXY")
	cmd.Env = buildDirectEnv("http://127.0.0.1:41900")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	if v := strings.TrimSpace(string(out)); v != "0" {
		t.Errorf("child saw NODE_USE_ENV_PROXY=%q, want 0", v)
	}
}

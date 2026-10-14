package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateUpstreamName(t *testing.T) {
	good := []string{"work", "my-proxy", "home_1", "abc", strings.Repeat("a", 32)}
	for _, n := range good {
		if err := ValidateUpstreamName(n); err != nil {
			t.Errorf("%q should be valid: %v", n, err)
		}
	}
	bad := map[string]string{
		"DE":                    "country code",
		"us":                    "country code",
		"ab":                    "country code",
		"a":                     "3 to 32",
		"Work":                  "3 to 32",
		"my proxy":              "3 to 32",
		"proxy.one":             "3 to 32",
		strings.Repeat("a", 33): "3 to 32",
		"":                      "3 to 32",
		"user:pass@host":        "3 to 32",
	}
	for n, want := range bad {
		err := ValidateUpstreamName(n)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want containing %q", n, err, want)
		}
	}
}

func TestUpstreamAddFindRemove(t *testing.T) {
	cfg := Default()
	if replaced := cfg.AddUpstream(Upstream{Name: "work", URL: "http://u:p@h:8080", Country: "DE"}); replaced {
		t.Error("first add must not report a replacement")
	}
	if replaced := cfg.AddUpstream(Upstream{Name: "home", URL: "socks5://h:1080"}); replaced {
		t.Error("second name must not report a replacement")
	}
	if replaced := cfg.AddUpstream(Upstream{Name: "work", URL: "http://u:new@h:8080"}); !replaced {
		t.Error("same name must replace")
	}
	if len(cfg.Upstreams) != 2 {
		t.Fatalf("len = %d, want 2", len(cfg.Upstreams))
	}
	u, ok := cfg.FindUpstream("work")
	if !ok || u.URL != "http://u:new@h:8080" || u.Country != "" {
		t.Errorf("FindUpstream(work) = %+v, %v", u, ok)
	}
	if _, ok := cfg.FindUpstream("nope"); ok {
		t.Error("unknown name should not be found")
	}
	if !cfg.RemoveUpstream("home") || cfg.RemoveUpstream("home") {
		t.Error("remove should succeed once, then fail")
	}
	if len(cfg.Upstreams) != 1 || cfg.Upstreams[0].Name != "work" {
		t.Errorf("after remove: %+v", cfg.Upstreams)
	}
}

func TestUpstreamsRoundTripThroughConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHELLROUTE_HOME", dir)
	t.Setenv("SHELLROUTE_API_KEY", "")

	cfg := Default()
	cfg.AddUpstream(Upstream{Name: "work", URL: "http://alice:s3cret@proxy.example:8080", Country: "DE", Note: "office"})
	cfg.AddUpstream(Upstream{Name: "home", URL: "socks5h://home.example:1080"})
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "config.toml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("config mode = %o, want 0600", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "[[upstream]]") {
		t.Errorf("config should use [[upstream]] tables:\n%s", raw)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Upstreams) != 2 {
		t.Fatalf("loaded %d upstreams, want 2:\n%s", len(loaded.Upstreams), raw)
	}
	w, ok := loaded.FindUpstream("work")
	if !ok || w.URL != "http://alice:s3cret@proxy.example:8080" || w.Country != "DE" || w.Note != "office" {
		t.Errorf("work = %+v", w)
	}
	h, _ := loaded.FindUpstream("home")
	if h == nil || h.Country != "" || h.Note != "" {
		t.Errorf("home = %+v", h)
	}
}

func TestNoUpstreamsLeavesConfigFileClean(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHELLROUTE_HOME", dir)
	if err := Save(Default()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if strings.Contains(string(raw), "upstream") {
		t.Errorf("empty upstream list should not appear in the file:\n%s", raw)
	}
}

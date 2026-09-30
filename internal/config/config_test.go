package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvVarWorksWithNoConfigFile(t *testing.T) {
	origKey := os.Getenv("SHELLROUTE_API_KEY")
	origHome := os.Getenv("SHELLROUTE_HOME")
	defer func() {
		os.Setenv("SHELLROUTE_API_KEY", origKey)
		os.Setenv("SHELLROUTE_HOME", origHome)
	}()

	// Point config dir at a temp dir with no config.toml
	os.Setenv("SHELLROUTE_HOME", t.TempDir())
	os.Setenv("SHELLROUTE_API_KEY", "pk_ci_test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.APIKey != "pk_ci_test" {
		t.Errorf("APIKey = %q, want pk_ci_test (env var must work without a config file)", cfg.APIKey)
	}
	if !cfg.HasAuth() {
		t.Error("HasAuth() should be true when SHELLROUTE_API_KEY is set")
	}
}

func TestEnvVarWorksWithoutHomeDir(t *testing.T) {
	// Bare containers and hardened CI images may run with no $HOME. The
	// config directory cannot be resolved, but SHELLROUTE_API_KEY must work.
	t.Setenv("HOME", "")
	t.Setenv("SHELLROUTE_HOME", "")
	t.Setenv("SHELLROUTE_API_KEY", "pk_ci_test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.APIKey != "pk_ci_test" {
		t.Errorf("APIKey = %q, want pk_ci_test (env var must work without $HOME)", cfg.APIKey)
	}
}

func TestEnvVarWorksWhenConfigDirUncreatable(t *testing.T) {
	// A config dir that cannot be created (here: nested under a regular file)
	// is equivalent to no config file; Load must not fail.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELLROUTE_HOME", filepath.Join(blocker, "sub"))
	t.Setenv("SHELLROUTE_API_KEY", "pk_ci_test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.APIKey != "pk_ci_test" {
		t.Errorf("APIKey = %q, want pk_ci_test (env var must work when config dir is uncreatable)", cfg.APIKey)
	}
}

func TestEnvVarOverridesConfig(t *testing.T) {
	// Save and restore env
	origKey := os.Getenv("SHELLROUTE_API_KEY")
	origURL := os.Getenv("SHELLROUTE_API_URL")
	defer func() {
		os.Setenv("SHELLROUTE_API_KEY", origKey)
		os.Setenv("SHELLROUTE_API_URL", origURL)
	}()

	os.Setenv("SHELLROUTE_API_KEY", "pk_from_env")
	os.Setenv("SHELLROUTE_API_URL", "https://custom.api.com")

	cfg := Default()
	cfg.APIKey = "pk_from_file"
	cfg.APIURL = "https://file.api.com"

	// Simulate what Load() does after reading config
	if envKey := os.Getenv("SHELLROUTE_API_KEY"); envKey != "" {
		cfg.APIKey = envKey
	}
	if envURL := os.Getenv("SHELLROUTE_API_URL"); envURL != "" {
		cfg.APIURL = envURL
	}

	if cfg.APIKey != "pk_from_env" {
		t.Errorf("APIKey = %q, want pk_from_env", cfg.APIKey)
	}
	if cfg.APIURL != "https://custom.api.com" {
		t.Errorf("APIURL = %q, want https://custom.api.com", cfg.APIURL)
	}
}

func TestEnvVarEmptyDoesNotOverride(t *testing.T) {
	origKey := os.Getenv("SHELLROUTE_API_KEY")
	defer os.Setenv("SHELLROUTE_API_KEY", origKey)

	os.Setenv("SHELLROUTE_API_KEY", "")

	cfg := Default()
	cfg.APIKey = "pk_from_file"

	if envKey := os.Getenv("SHELLROUTE_API_KEY"); envKey != "" {
		cfg.APIKey = envKey
	}

	if cfg.APIKey != "pk_from_file" {
		t.Errorf("empty env should not override, got %q", cfg.APIKey)
	}
}

func TestHasAuth(t *testing.T) {
	cfg := Default()
	if cfg.HasAuth() {
		t.Error("default config should not have auth")
	}
	cfg.APIKey = "pk_test"
	if !cfg.HasAuth() {
		t.Error("should have auth after setting key")
	}
}

func TestApplyFlag(t *testing.T) {
	cfg := Default()
	cfg.APIKey = "pk_from_file"

	// Empty flag doesn't override
	cfg.ApplyFlag("")
	if cfg.APIKey != "pk_from_file" {
		t.Errorf("empty flag should not override, got %q", cfg.APIKey)
	}

	// Non-empty flag overrides
	cfg.ApplyFlag("pk_from_flag")
	if cfg.APIKey != "pk_from_flag" {
		t.Errorf("flag should override, got %q", cfg.APIKey)
	}
}

func TestDefaultValues(t *testing.T) {
	cfg := Default()

	if cfg.DefaultPort != 41900 {
		t.Errorf("DefaultPort = %d, want 41900", cfg.DefaultPort)
	}
	if cfg.DefaultType != "residential" {
		t.Errorf("DefaultType = %q, want residential", cfg.DefaultType)
	}
	if cfg.APIURL != "https://api.shellroute.com" {
		t.Errorf("APIURL = %q", cfg.APIURL)
	}
	if !cfg.UsageWarnings {
		t.Error("UsageWarnings should default to true")
	}
}

func TestValidateCountry(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"US", false},
		{"GB", false},
		{"DE", false},
		{"JP", false},
		{"", true},        // empty
		{"A", true},       // too short
		{"USA", true},     // too long
		{"us", true},      // lowercase (NormalizeCountry should be called first)
		{"U1", true},      // digit
		{"U ", true},      // space
		{"<>", true},      // brackets
		{"'; DROP", true}, // SQL injection
	}
	for _, tt := range tests {
		err := ValidateCountry(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateCountry(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
		}
	}
}

func TestValidateIPType(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"", false}, // empty = default
		{"residential", false},
		{"datacenter", false},
		{"mix", false},
		{"Residential", true}, // case-sensitive
		{"DATACENTER", true},
		{"garbage", true},
		{"<script>", true},
	}
	for _, tt := range tests {
		err := ValidateIPType(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateIPType(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
		}
	}
}

func TestValidateCity(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"", false},
		{"New York", false},
		{"Los Angeles", false},
		{"<script>alert(1)</script>", true}, // XSS
		{"'; DROP TABLE", true},             // SQL injection chars
		{"city\"name", true},                // quotes
	}
	for _, tt := range tests {
		err := ValidateCity(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateCity(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
		}
	}
}

func TestValidateFormat(t *testing.T) {
	tests := []struct {
		format  string
		allowed []string
		wantErr bool
	}{
		{"human", []string{"human", "json"}, false},
		{"json", []string{"human", "json"}, false},
		{"env", []string{"human", "json", "env"}, false},
		{"xml", []string{"human", "json"}, true},
		{"yaml", []string{"human", "json"}, true},
		{"", []string{"human", "json"}, true},
		{"JSON", []string{"human", "json"}, true}, // case-sensitive
		{"csv", []string{"human", "json"}, true},
		{"human", []string{}, true}, // no allowed values
	}
	for _, tt := range tests {
		err := ValidateFormat(tt.format, tt.allowed...)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateFormat(%q, %v) error = %v, wantErr = %v", tt.format, tt.allowed, err, tt.wantErr)
		}
	}
}

func TestNormalizeThenValidate(t *testing.T) {
	// Full pipeline: normalize then validate
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"us", false}, // lowercase → US → valid
		{"UK", false}, // alias → GB → valid
		{"uk", false}, // lowercase alias → GB → valid
		{"XY", false}, // unknown but valid format
		{"", true},    // empty
	}
	for _, tt := range tests {
		normalized := NormalizeCountry(tt.input)
		err := ValidateCountry(normalized)
		if (err != nil) != tt.wantErr {
			t.Errorf("Normalize(%q)=%q Validate error=%v, wantErr=%v",
				tt.input, normalized, err, tt.wantErr)
		}
	}
}

func TestNormalizeCountry(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"UK", "GB"},
		{"uk", "GB"},
		{"Uk", "GB"},
		{"GB", "GB"},
		{"gb", "GB"},
		{"US", "US"},
		{"us", "US"},
		{"de", "DE"},
		{"", ""},
	}
	for _, tt := range tests {
		got := NormalizeCountry(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeCountry(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

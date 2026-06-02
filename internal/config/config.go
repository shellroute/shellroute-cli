package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	DefaultPort     = 41900 // high port, unlikely to conflict with dev services
	DefaultType     = "residential"
	DefaultProtocol = "http"
	DefaultAPIURL   = "https://api.shellroute.com"
	DefaultLogLevel = "info"
)

type Config struct {
	APIKey          string `toml:"api_key"`
	DefaultCountry  string `toml:"default_country,omitempty"`
	DefaultPort     int    `toml:"default_port"`
	DefaultType     string `toml:"default_type"`
	DefaultProtocol string `toml:"default_protocol"`
	APIURL          string `toml:"api_url"`
	LogLevel        string `toml:"log_level"`
	UsageWarnings   bool   `toml:"usage_warnings"`
	AutoDisconnect  int    `toml:"auto_disconnect_at"`
}

// IsLocalMode returns true if ~/.shellroute-mode contains "local".
func IsLocalMode() bool {
	if envDir := os.Getenv("SHELLROUTE_HOME"); envDir != "" {
		return false // explicit env override takes priority
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(home, ".shellroute-mode"))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "local"
}

func Default() *Config {
	return &Config{
		DefaultPort:     DefaultPort,
		DefaultType:     DefaultType,
		DefaultProtocol: DefaultProtocol,
		APIURL:          DefaultAPIURL,
		LogLevel:        DefaultLogLevel,
		UsageWarnings:   true,
	}
}

// Dir returns the config directory, creating it if needed (mode 700).
// Default: ~/.shellroute. In local mode (via ~/.shellroute-mode or
// SHELLROUTE_HOME env var): ~/.shellroute-local.
func Dir() (string, error) {
	if envDir := os.Getenv("SHELLROUTE_HOME"); envDir != "" {
		if err := os.MkdirAll(envDir, 0700); err != nil {
			return "", fmt.Errorf("cannot create config directory: %w", err)
		}
		return envDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find home directory: %w", err)
	}
	dirName := ".shellroute"
	if IsLocalMode() {
		dirName = ".shellroute-local"
	}
	dir := filepath.Join(home, dirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cannot create config directory: %w", err)
	}
	return dir, nil
}

// Path returns the full path to config.toml.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// Load reads config from ~/.shellroute/config.toml. Returns defaults if file doesn't exist.
func Load() (*Config, error) {
	cfg := Default()
	path, err := Path()
	if err != nil {
		return cfg, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("cannot read config: %w", err)
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return cfg, fmt.Errorf("cannot parse config: %w", err)
	}

	// Apply defaults for zero values
	if cfg.DefaultPort == 0 {
		cfg.DefaultPort = DefaultPort
	}
	if cfg.DefaultType == "" {
		cfg.DefaultType = DefaultType
	}
	if cfg.DefaultProtocol == "" {
		cfg.DefaultProtocol = DefaultProtocol
	}
	if cfg.APIURL == "" {
		cfg.APIURL = DefaultAPIURL
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = DefaultLogLevel
	}

	// Env var overrides config file (for agents/CI)
	if envKey := os.Getenv("SHELLROUTE_API_KEY"); envKey != "" {
		cfg.APIKey = envKey
	}
	if envURL := os.Getenv("SHELLROUTE_API_URL"); envURL != "" {
		cfg.APIURL = envURL
	}

	return cfg, nil
}

// ApplyFlag overrides API key from CLI flag (highest priority).
func (c *Config) ApplyFlag(apiKey string) {
	if apiKey != "" {
		c.APIKey = apiKey
	}
}

// Save writes config to ~/.shellroute/config.toml with 600 permissions.
func Save(cfg *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("cannot write config: %w", err)
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("cannot encode config: %w", err)
	}
	return nil
}

// HasAuth returns true if an API key is configured.
func (c *Config) HasAuth() bool {
	return c.APIKey != ""
}

// countryAliases maps common non-ISO codes to their ISO 3166-1 alpha-2 equivalent.
var countryAliases = map[string]string{
	"UK": "GB",
}

// NormalizeCountry uppercases and resolves country code aliases (e.g. UK → GB).
func NormalizeCountry(code string) string {
	code = strings.ToUpper(code)
	if mapped, ok := countryAliases[code]; ok {
		return mapped
	}
	return code
}

// ValidateCountry checks that a country code looks like a valid ISO 3166-1 alpha-2 code.
// Does NOT check against the locations table — server does that.
func ValidateCountry(code string) error {
	if len(code) != 2 {
		return fmt.Errorf("country code must be exactly 2 letters (e.g. US, DE, GB), got %q", code)
	}
	for _, c := range code {
		if c < 'A' || c > 'Z' {
			return fmt.Errorf("country code must be 2 uppercase letters, got %q", code)
		}
	}
	return nil
}

// ValidateIPType checks that iptype is one of the supported values.
func ValidateIPType(ipType string) error {
	switch ipType {
	case "", "residential", "datacenter", "mix":
		return nil
	default:
		return fmt.Errorf("invalid IP type %q — must be residential, datacenter, or mix", ipType)
	}
}

// ValidateFormat checks that an output format is one of the supported values.
func ValidateFormat(format string, allowed ...string) error {
	for _, a := range allowed {
		if format == a {
			return nil
		}
	}
	return fmt.Errorf("invalid format %q — supported: %s", format, strings.Join(allowed, ", "))
}

// ValidateCity checks that a city name is reasonable (no injection, reasonable length).
func ValidateCity(city string) error {
	if len(city) > 100 {
		return fmt.Errorf("city name too long (max 100 chars)")
	}
	for _, c := range city {
		if c < 0x20 || c == '<' || c == '>' || c == '\'' || c == '"' || c == ';' {
			return fmt.Errorf("city name contains invalid characters")
		}
	}
	return nil
}

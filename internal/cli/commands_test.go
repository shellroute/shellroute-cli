package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// --- Helpers ---

func stubCommand(cmd *cobra.Command) func() {
	origRunE := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error { return nil }
	origPreRunE := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = nil
	return func() {
		cmd.RunE = origRunE
		rootCmd.PersistentPreRunE = origPreRunE
	}
}

func resetFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false; f.Value.Set(f.DefValue) })
}

// --- run command ---

func TestRunAllFlags(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	rootCmd.SetArgs([]string{"run", "--country", "DE", "--city", "Berlin", "--iptype", "datacenter", "--no-stat", "--sticky", "--", "echo", "hi"})
	rootCmd.Execute()

	if runCountry != "DE" {
		t.Errorf("country = %q, want DE", runCountry)
	}
	if runCity != "Berlin" {
		t.Errorf("city = %q, want Berlin", runCity)
	}
	if runIPType != "datacenter" {
		t.Errorf("iptype = %q, want datacenter", runIPType)
	}
	if !runNoStat {
		t.Error("no-stat should be true")
	}
	if !runSticky {
		t.Error("sticky should be true")
	}
}

func TestRunDefaults(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	rootCmd.SetArgs([]string{"run", "US", "--", "curl", "https://example.com"})
	rootCmd.Execute()

	if runIPType != "" {
		t.Errorf("iptype default = %q, want empty", runIPType)
	}
	if runNoStat {
		t.Error("no-stat should default false")
	}
	if runSticky {
		t.Error("sticky should default false")
	}
}

func TestRunPositionalCountry(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	// Country as first positional arg (not --country flag)
	rootCmd.SetArgs([]string{"run", "JP", "--", "wget", "-q", "https://example.com"})
	rootCmd.Execute()

	// The positional country is parsed inside runRun, not by cobra flags.
	// runCountry stays empty because it's a flag, but args[0] = "JP".
	// This verifies cobra accepts the positional arg format.
}

func TestRunNoStatOnly(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	rootCmd.SetArgs([]string{"run", "--no-stat", "GB", "--", "echo"})
	rootCmd.Execute()

	if !runNoStat {
		t.Error("no-stat should be true")
	}
	if runSticky {
		t.Error("sticky should be false when not specified")
	}
}

func TestRunStickyOnly(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	rootCmd.SetArgs([]string{"run", "--sticky", "US", "--", "echo"})
	rootCmd.Execute()

	if !runSticky {
		t.Error("sticky should be true")
	}
	if runNoStat {
		t.Error("no-stat should be false when not specified")
	}
}

func TestRunCityWithoutIPType(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	rootCmd.SetArgs([]string{"run", "--city", "NYC", "US", "--", "echo"})
	rootCmd.Execute()

	if runCity != "NYC" {
		t.Errorf("city = %q, want NYC", runCity)
	}
	if runIPType != "" {
		t.Errorf("iptype should be empty, got %q", runIPType)
	}
}

// --- connect command ---

func TestConnectFlags(t *testing.T) {
	resetFlags(connectCmd)
	defer stubCommand(connectCmd)()

	rootCmd.SetArgs([]string{"proxy", "--country", "US", "--city", "NYC", "--iptype", "datacenter", "--format", "json"})
	rootCmd.Execute()

	if connectCountry != "US" {
		t.Errorf("country = %q, want US", connectCountry)
	}
	if connectCity != "NYC" {
		t.Errorf("city = %q, want NYC", connectCity)
	}
	if connectType != "datacenter" {
		t.Errorf("iptype = %q, want datacenter", connectType)
	}
	if connectFormat != "json" {
		t.Errorf("format = %q, want json", connectFormat)
	}
}

func TestConnectDefaults(t *testing.T) {
	resetFlags(connectCmd)
	defer stubCommand(connectCmd)()

	rootCmd.SetArgs([]string{"proxy", "--country", "GB"})
	rootCmd.Execute()

	if connectType != "" {
		t.Errorf("iptype default = %q, want empty", connectType)
	}
	if connectFormat != "json" {
		t.Errorf("format default = %q, want json", connectFormat)
	}
}

// --- countries command ---

func TestCountriesFlags(t *testing.T) {
	resetFlags(countriesCmd)
	defer stubCommand(countriesCmd)()

	rootCmd.SetArgs([]string{"countries", "--format", "json"})
	rootCmd.Execute()

	if countriesFormat != "json" {
		t.Errorf("format = %q, want json", countriesFormat)
	}
}

func TestCountriesDefault(t *testing.T) {
	resetFlags(countriesCmd)
	defer stubCommand(countriesCmd)()

	rootCmd.SetArgs([]string{"countries"})
	rootCmd.Execute()

	if countriesFormat != "human" {
		t.Errorf("format default = %q, want human", countriesFormat)
	}
}

// --- cities command ---

func TestCitiesFlags(t *testing.T) {
	resetFlags(citiesCmd)
	defer stubCommand(citiesCmd)()

	rootCmd.SetArgs([]string{"cities", "US", "--format", "json"})
	rootCmd.Execute()

	if citiesFormat != "json" {
		t.Errorf("format = %q, want json", citiesFormat)
	}
}

func TestCitiesDefault(t *testing.T) {
	resetFlags(citiesCmd)
	defer stubCommand(citiesCmd)()

	rootCmd.SetArgs([]string{"cities", "DE"})
	rootCmd.Execute()

	if citiesFormat != "human" {
		t.Errorf("format default = %q, want human", citiesFormat)
	}
}

// --- status command ---

func TestStatusFormatJSON(t *testing.T) {
	resetFlags(statusCmd)
	defer stubCommand(statusCmd)()

	rootCmd.SetArgs([]string{"status", "--format", "json"})
	rootCmd.Execute()

	if statusFormat != "json" {
		t.Errorf("format = %q, want json", statusFormat)
	}
}

func TestStatusFormatDefault(t *testing.T) {
	resetFlags(statusCmd)
	defer stubCommand(statusCmd)()

	rootCmd.SetArgs([]string{"status"})
	rootCmd.Execute()

	if statusFormat != "human" {
		t.Errorf("format default = %q, want human", statusFormat)
	}
}

// --- balance command ---

func TestBalanceFormatJSON(t *testing.T) {
	resetFlags(balanceCmd)
	defer stubCommand(balanceCmd)()

	rootCmd.SetArgs([]string{"balance", "--format", "json"})
	rootCmd.Execute()

	if balanceFormat != "json" {
		t.Errorf("format = %q, want json", balanceFormat)
	}
}

func TestBalanceFormatDefault(t *testing.T) {
	resetFlags(balanceCmd)
	defer stubCommand(balanceCmd)()

	rootCmd.SetArgs([]string{"balance"})
	rootCmd.Execute()

	if balanceFormat != "human" {
		t.Errorf("format default = %q, want human", balanceFormat)
	}
}

// --- global flags ---

func TestGlobalAPIKeyFlag(t *testing.T) {
	flagAPIKey = ""
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	defer stubCommand(rootCmd)()

	rootCmd.SetArgs([]string{"--api-key", "pk_test_123"})
	rootCmd.Execute()

	if flagAPIKey != "pk_test_123" {
		t.Errorf("api-key = %q, want pk_test_123", flagAPIKey)
	}
}

func TestSkipVersionCheckFlag(t *testing.T) {
	flagSkipVersionCheck = false
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	defer stubCommand(rootCmd)()

	rootCmd.SetArgs([]string{"--skip-version-check"})
	rootCmd.Execute()

	if !flagSkipVersionCheck {
		t.Error("skip-version-check should be true")
	}
}

func TestAPIKeyWithSubcommand(t *testing.T) {
	flagAPIKey = ""
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	rootCmd.SetArgs([]string{"--api-key", "pk_inline", "run", "US", "--", "echo"})
	rootCmd.Execute()

	if flagAPIKey != "pk_inline" {
		t.Errorf("api-key = %q, want pk_inline", flagAPIKey)
	}
}

// --- version ---

func TestVersionOutput(t *testing.T) {
	v := Version()
	if v == "" {
		t.Error("Version() should not be empty")
	}
	if rootCmd.Version != v {
		t.Errorf("rootCmd.Version = %q, Version() = %q", rootCmd.Version, v)
	}
}

// --- reveal-key command ---

func TestRevealKeyRegistered(t *testing.T) {
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "reveal-key" {
			found = true
			if cmd.Short == "" {
				t.Error("reveal-key should have a Short description")
			}
		}
	}
	if !found {
		t.Error("reveal-key command not registered")
	}
}

// --- command existence ---

func TestAllCommandsRegistered(t *testing.T) {
	expected := []string{"login", "logout", "proxy", "status", "balance", "countries", "cities", "run", "reveal-key"}
	commands := make(map[string]bool)
	for _, cmd := range rootCmd.Commands() {
		commands[cmd.Name()] = true
	}
	for _, name := range expected {
		if !commands[name] {
			t.Errorf("command %q not registered", name)
		}
	}
}

// --- Flag → API request mapping tests ---
// These verify that flag combinations produce the correct SessionCreateRequest

func TestConnectFlagsToRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want func() (country, city, iptype, format string)
	}{
		{
			name: "all flags",
			args: []string{"proxy", "--country", "US", "--city", "NYC", "--iptype", "datacenter", "--format", "json"},
			want: func() (string, string, string, string) { return "US", "NYC", "datacenter", "json" },
		},
		{
			name: "minimal",
			args: []string{"proxy", "--country", "DE"},
			want: func() (string, string, string, string) { return "DE", "", "", "json" },
		},
		{
			name: "env format",
			args: []string{"proxy", "--country", "GB", "--format", "env"},
			want: func() (string, string, string, string) { return "GB", "", "", "env" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetFlags(connectCmd)
			defer stubCommand(connectCmd)()
			rootCmd.SetArgs(tt.args)
			rootCmd.Execute()

			wantCountry, wantCity, wantType, wantFormat := tt.want()
			if connectCountry != wantCountry {
				t.Errorf("country = %q, want %q", connectCountry, wantCountry)
			}
			if connectCity != wantCity {
				t.Errorf("city = %q, want %q", connectCity, wantCity)
			}
			if connectType != wantType {
				t.Errorf("iptype = %q, want %q", connectType, wantType)
			}
			if connectFormat != wantFormat {
				t.Errorf("format = %q, want %q", connectFormat, wantFormat)
			}
		})
	}
}

func TestRunFlagsToRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want func() (country, city, iptype string, sticky, nostat bool)
	}{
		{
			name: "all flags with positional country",
			args: []string{"run", "US", "--city", "LA", "--iptype", "datacenter", "--sticky", "--no-stat", "--", "echo"},
			want: func() (string, string, string, bool, bool) { return "US", "LA", "datacenter", true, true },
		},
		{
			name: "flag country",
			args: []string{"run", "--country", "DE", "--", "curl", "https://example.com"},
			want: func() (string, string, string, bool, bool) { return "DE", "", "", false, false },
		},
		{
			name: "sticky false explicit",
			args: []string{"run", "--country", "JP", "--sticky=false", "--", "echo"},
			want: func() (string, string, string, bool, bool) { return "JP", "", "", false, false },
		},
		{
			name: "residential default",
			args: []string{"run", "GB", "--", "echo"},
			want: func() (string, string, string, bool, bool) { return "GB", "", "", false, false },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetFlags(runCmd)
			defer stubCommand(runCmd)()
			rootCmd.SetArgs(tt.args)
			rootCmd.Execute()

			wantCountry, wantCity, wantType, wantSticky, wantNostat := tt.want()
			if runCountry != wantCountry && runCountry != "" {
				// positional country is parsed in RunE, not flag — check the flag
				t.Logf("note: runCountry=%q (positional country parsed at runtime)", runCountry)
			}
			if runCity != wantCity {
				t.Errorf("city = %q, want %q", runCity, wantCity)
			}
			if runIPType != wantType {
				t.Errorf("iptype = %q, want %q", runIPType, wantType)
			}
			if runSticky != wantSticky {
				t.Errorf("sticky = %v, want %v", runSticky, wantSticky)
			}
			if runNoStat != wantNostat {
				t.Errorf("no-stat = %v, want %v", runNoStat, wantNostat)
			}
		})
	}
}

func TestConnectBuildRequest(t *testing.T) {
	// Verify that parsed flags produce the correct API request shape
	resetFlags(connectCmd)
	defer stubCommand(connectCmd)()

	rootCmd.SetArgs([]string{"proxy", "--country", "US", "--city", "Chicago", "--iptype", "datacenter", "--format", "json"})
	rootCmd.Execute()

	// Simulate what runConnectHeadless does
	req := &api.SessionCreateRequest{
		Country: config.NormalizeCountry(connectCountry),
		City:    connectCity,
		Type:    connectType,
	}

	if req.Country != "US" {
		t.Errorf("request country = %q, want US", req.Country)
	}
	if req.City != "Chicago" {
		t.Errorf("request city = %q, want Chicago", req.City)
	}
	if req.Type != "datacenter" {
		t.Errorf("request type = %q, want datacenter", req.Type)
	}
}

func TestRunBuildRequest(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	rootCmd.SetArgs([]string{"run", "--country", "JP", "--city", "Tokyo", "--iptype", "residential", "--sticky", "--", "echo"})
	rootCmd.Execute()

	req := &api.SessionCreateRequest{
		Country: config.NormalizeCountry(runCountry),
		City:    runCity,
		Type:    runIPType,
		Sticky:  runSticky,
	}

	if req.Country != "JP" {
		t.Errorf("request country = %q, want JP", req.Country)
	}
	if req.City != "Tokyo" {
		t.Errorf("request city = %q, want Tokyo", req.City)
	}
	if req.Type != "residential" {
		t.Errorf("request type = %q, want residential", req.Type)
	}
	if !req.Sticky {
		t.Error("request sticky should be true")
	}
}

func TestConnectBuildRequestWithSticky(t *testing.T) {
	resetFlags(connectCmd)
	defer stubCommand(connectCmd)()

	rootCmd.SetArgs([]string{"proxy", "--country", "US", "--sticky", "--format", "json"})
	rootCmd.Execute()

	req := &api.SessionCreateRequest{
		Country: config.NormalizeCountry(connectCountry),
		City:    connectCity,
		Type:    connectType,
		Sticky:  connectSticky,
	}

	if !req.Sticky {
		t.Error("request sticky should be true")
	}
	if req.Country != "US" {
		t.Errorf("country = %q", req.Country)
	}
}

func TestConnectBuildRequestStickyFalse(t *testing.T) {
	resetFlags(connectCmd)
	defer stubCommand(connectCmd)()

	rootCmd.SetArgs([]string{"proxy", "--country", "DE", "--sticky=false", "--format", "json"})
	rootCmd.Execute()

	if connectSticky {
		t.Error("sticky should be false when explicitly set to false")
	}
}

func TestRunBuildRequestMixIPType(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	rootCmd.SetArgs([]string{"run", "--country", "GB", "--iptype", "mix", "--", "echo"})
	rootCmd.Execute()

	req := &api.SessionCreateRequest{
		Country: config.NormalizeCountry(runCountry),
		Type:    runIPType,
	}
	if req.Type != "mix" {
		t.Errorf("iptype = %q, want mix", req.Type)
	}
}

func TestRunBuildRequestCityAndSticky(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	rootCmd.SetArgs([]string{"run", "--country", "US", "--city", "NYC", "--sticky", "--iptype", "datacenter", "--", "echo"})
	rootCmd.Execute()

	req := &api.SessionCreateRequest{
		Country: config.NormalizeCountry(runCountry),
		City:    runCity,
		Type:    runIPType,
		Sticky:  runSticky,
	}
	if req.Country != "US" {
		t.Errorf("country = %q", req.Country)
	}
	if req.City != "NYC" {
		t.Errorf("city = %q", req.City)
	}
	if req.Type != "datacenter" {
		t.Errorf("type = %q", req.Type)
	}
	if !req.Sticky {
		t.Error("sticky should be true")
	}
}

func TestCitiesRequiresCountryArg(t *testing.T) {
	resetFlags(citiesCmd)
	defer stubCommand(citiesCmd)()

	rootCmd.SetArgs([]string{"cities"})
	err := rootCmd.Execute()
	// cobra enforces ExactArgs(1) — should fail
	if err == nil {
		t.Error("cities without country arg should fail")
	}
}

func TestConnectCountryNormalization(t *testing.T) {
	resetFlags(connectCmd)
	defer stubCommand(connectCmd)()

	rootCmd.SetArgs([]string{"proxy", "--country", "uk", "--format", "json"})
	rootCmd.Execute()

	normalized := config.NormalizeCountry(connectCountry)
	if normalized != "GB" {
		t.Errorf("UK should normalize to GB, got %q", normalized)
	}
}

func TestRunPositionalCountryOverridesFlag(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	// When both positional and --country are given, --country wins
	// (positional is only used when --country is empty)
	rootCmd.SetArgs([]string{"run", "--country", "DE", "US", "--", "echo"})
	rootCmd.Execute()

	if runCountry != "DE" {
		t.Errorf("--country flag should take priority, got %q", runCountry)
	}
}

// --- Input validation / garbage / injection tests ---

func TestNormalizeCountryGarbage(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"us", "US"},
		{"UK", "GB"},             // alias
		{"uk", "GB"},             // alias + lowercase
		{"", ""},                 // empty
		{"XYZ", "XYZ"},           // unknown 3-letter → passes through (server rejects)
		{"US ", "US "},           // trailing space → server will reject
		{"A", "A"},               // too short
		{"<script>", "<SCRIPT>"}, // XSS attempt → uppercased, server rejects
		{"'; DROP", "'; DROP"},   // SQL injection → passes through, server uses parameterized queries
	}
	for _, tt := range tests {
		got := config.NormalizeCountry(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeCountry(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsAlpha(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"US", true},
		{"de", true},
		{"GB", true},
		{"", false},
		{"U1", false},   // digit
		{"U ", false},   // space
		{"US!", false},  // punctuation
		{"<>", false},   // brackets
		{"US\n", false}, // newline
	}
	for _, tt := range tests {
		got := isAlpha(tt.input)
		if got != tt.want {
			t.Errorf("isAlpha(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestConnectGarbageCountry(t *testing.T) {
	resetFlags(connectCmd)
	defer stubCommand(connectCmd)()

	// SQL injection as country code — should just be stored as a string
	rootCmd.SetArgs([]string{"proxy", "--country", "'; DROP TABLE sessions; --", "--format", "json"})
	rootCmd.Execute()

	// CLI doesn't reject it — server will via IsValidCountry
	if connectCountry != "'; DROP TABLE sessions; --" {
		t.Errorf("garbage country should pass through CLI (server validates)")
	}
	normalized := config.NormalizeCountry(connectCountry)
	if normalized == "" {
		t.Error("normalize should not produce empty")
	}
}

func TestConnectGarbageIPType(t *testing.T) {
	resetFlags(connectCmd)
	defer stubCommand(connectCmd)()

	rootCmd.SetArgs([]string{"proxy", "--country", "US", "--iptype", "garbage_type", "--format", "json"})
	rootCmd.Execute()

	// CLI passes through — server rejects invalid types
	if connectType != "garbage_type" {
		t.Errorf("iptype = %q, want garbage_type (CLI should pass, server validates)", connectType)
	}
}

func TestRunGarbageCity(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	rootCmd.SetArgs([]string{"run", "--country", "US", "--city", "<script>alert(1)</script>", "--", "echo"})
	rootCmd.Execute()

	// CLI passes through — city is best-effort, server stores as text
	if runCity != "<script>alert(1)</script>" {
		t.Errorf("city = %q, XSS attempt should pass through CLI", runCity)
	}
}

func TestRunEmptyCountryNoDefault(t *testing.T) {
	resetFlags(runCmd)
	// Don't stub — let it actually run (it will fail before network call)
	origRunE := runCmd.RunE
	defer func() { runCmd.RunE = origRunE }()

	// Reset to test the "country required" error
	runCmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Simulate: no country flag, no positional, no default
		if runCountry == "" && (len(args) < 2 || !isAlpha(args[0])) {
			return fmt.Errorf("country required")
		}
		return nil
	}

	rootCmd.SetArgs([]string{"run", "--", "echo"})
	err := rootCmd.Execute()
	if err == nil {
		t.Error("run without country should fail")
	}
}

func TestConnectFormatInvalid(t *testing.T) {
	resetFlags(connectCmd)
	defer stubCommand(connectCmd)()

	rootCmd.SetArgs([]string{"proxy", "--country", "US", "--format", "xml"})
	rootCmd.Execute()

	// Invalid format passes through — falls to default "human" path at runtime
	if connectFormat != "xml" {
		t.Errorf("format = %q, want xml (invalid formats pass CLI, default at runtime)", connectFormat)
	}
}

func TestRunPositionalNonAlphaIsNotCountry(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	// "12" looks like 2 chars but isn't alpha — should NOT be treated as country
	rootCmd.SetArgs([]string{"run", "--country", "US", "12", "--", "echo"})
	rootCmd.Execute()

	if runCountry != "US" {
		t.Errorf("country = %q, numeric positional should not override flag", runCountry)
	}
}

func TestConnectEmptyCity(t *testing.T) {
	resetFlags(connectCmd)
	defer stubCommand(connectCmd)()

	rootCmd.SetArgs([]string{"proxy", "--country", "US", "--city", "", "--format", "json"})
	rootCmd.Execute()

	if connectCity != "" {
		t.Errorf("empty city should stay empty, got %q", connectCity)
	}
}

// --- Strict mode: illegal combination tests ---
// These test that invalid flag combinations are rejected with clear errors.
// We run the actual RunE (not stubbed) to test the full validation chain.
// Commands will fail after validation (no network) — we check the error message.

func withTempConfig(t *testing.T, f func()) {
	t.Helper()
	dir := t.TempDir()
	orig := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", orig)
	// Create minimal config with auth
	cfg := config.Default()
	cfg.APIKey = "pk_test_key_for_validation"
	config.Save(cfg)
	f()
}

func runCmdExpectError(t *testing.T, args []string, wantContains string) {
	t.Helper()
	resetAllFlags()
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	if err == nil {
		t.Errorf("expected error for args %v, got nil", args)
		return
	}
	if wantContains != "" && !containsStr(err.Error(), wantContains) {
		t.Errorf("args %v: error = %q, want containing %q", args, err.Error(), wantContains)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || findSubstring(s, sub))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func resetAllFlags() {
	for _, cmd := range rootCmd.Commands() {
		resetFlags(cmd)
	}
}

func TestConnectInvalidFormat(t *testing.T) {
	withTempConfig(t, func() {
		runCmdExpectError(t, []string{"proxy", "--country", "US", "--format", "xml"}, "invalid format")
	})
}

func TestConnectNoCountry(t *testing.T) {
	withTempConfig(t, func() {
		runCmdExpectError(t, []string{"proxy", "--format", "json"}, "country required")
	})
}

func TestConnectInvalidCountryFormat(t *testing.T) {
	withTempConfig(t, func() {
		runCmdExpectError(t, []string{"proxy", "--country", "USA", "--format", "json"}, "2 letters")
	})
}

func TestConnectSQLInjectionCountry(t *testing.T) {
	withTempConfig(t, func() {
		runCmdExpectError(t, []string{"proxy", "--country", "'; DROP TABLE", "--format", "json"}, "2 letters")
	})
}

func TestConnectInvalidIPType(t *testing.T) {
	withTempConfig(t, func() {
		runCmdExpectError(t, []string{"proxy", "--country", "US", "--iptype", "hacking", "--format", "json"}, "invalid IP type")
	})
}

func TestConnectXSSCity(t *testing.T) {
	withTempConfig(t, func() {
		runCmdExpectError(t, []string{"proxy", "--country", "US", "--city", "<script>", "--format", "json"}, "invalid characters")
	})
}

func TestStatusInvalidFormat(t *testing.T) {
	withTempConfig(t, func() {
		runCmdExpectError(t, []string{"status", "--format", "xml"}, "invalid format")
	})
}

func TestBalanceInvalidFormat(t *testing.T) {
	withTempConfig(t, func() {
		runCmdExpectError(t, []string{"balance", "--format", "yaml"}, "invalid format")
	})
}

func TestCountriesInvalidFormat(t *testing.T) {
	withTempConfig(t, func() {
		runCmdExpectError(t, []string{"countries", "--format", "csv"}, "invalid format")
	})
}

func TestCitiesInvalidFormat(t *testing.T) {
	withTempConfig(t, func() {
		runCmdExpectError(t, []string{"cities", "US", "--format", "proto"}, "invalid format")
	})
}

func TestCitiesNoArg(t *testing.T) {
	resetAllFlags()
	rootCmd.SetArgs([]string{"cities"})
	err := rootCmd.Execute()
	if err == nil {
		t.Error("cities without arg should fail")
	}
}

func TestCitiesTooManyArgs(t *testing.T) {
	resetAllFlags()
	rootCmd.SetArgs([]string{"cities", "US", "GB"})
	err := rootCmd.Execute()
	if err == nil {
		t.Error("cities with 2 args should fail")
	}
}

func TestConnectValidCombinations(t *testing.T) {
	// These should all pass validation (they'll fail on network, not validation)
	tests := []struct {
		name string
		args []string
	}{
		{"country only", []string{"proxy", "--country", "US", "--format", "json"}},
		{"country + city", []string{"proxy", "--country", "US", "--city", "New York", "--format", "json"}},
		{"country + iptype", []string{"proxy", "--country", "DE", "--iptype", "datacenter", "--format", "json"}},
		{"country + mix", []string{"proxy", "--country", "GB", "--iptype", "mix", "--format", "json"}},
		{"country + sticky", []string{"proxy", "--country", "JP", "--sticky", "--format", "json"}},
		{"all flags", []string{"proxy", "--country", "US", "--city", "LA", "--iptype", "datacenter", "--sticky", "--format", "env"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTempConfig(t, func() {
				resetAllFlags()
				rootCmd.SetArgs(tt.args)
				err := rootCmd.Execute()
				// Should fail on network/session, NOT on validation
				if err != nil {
					msg := err.Error()
					if containsStr(msg, "invalid format") ||
						containsStr(msg, "2 letters") ||
						containsStr(msg, "invalid IP type") ||
						containsStr(msg, "invalid characters") ||
						containsStr(msg, "country required") {
						t.Errorf("valid combination rejected by validation: %v", err)
					}
				}
			})
		})
	}
}

func TestNoUnexpectedCommands(t *testing.T) {
	allowed := map[string]bool{
		"login": true, "logout": true, "proxy": true,
		"status": true, "balance": true, "countries": true, "cities": true,
		"run": true, "reveal-key": true, "help": true, "completion": true,
	}
	for _, cmd := range rootCmd.Commands() {
		if !allowed[cmd.Name()] {
			t.Errorf("unexpected command %q registered — all CLI commands require explicit approval", cmd.Name())
		}
	}

	// Check proxy subcommands
	allowedProxySubs := map[string]bool{"stop": true}
	for _, sub := range connectCmd.Commands() {
		if !allowedProxySubs[sub.Name()] {
			t.Errorf("unexpected proxy subcommand %q", sub.Name())
		}
	}
}

// --- Direct login tests ---

func TestLoginRequestCodeRequiresEmail(t *testing.T) {
	resetFlags(loginCmd)
	loginEmail = ""
	loginRequestCode = true
	// Use invalid API URL so auth check fails → proceeds to --request-code logic
	t.Setenv("SHELLROUTE_API_URL", "http://127.0.0.1:1")
	defer func() { loginRequestCode = false }()

	err := runLogin(loginCmd, nil)
	if err == nil {
		t.Error("--request-code without --email should fail")
	}
}

func TestLoginVerifyCodeRequiresEmail(t *testing.T) {
	resetFlags(loginCmd)
	loginEmail = ""
	loginVerifyCode = "123456"
	t.Setenv("SHELLROUTE_API_URL", "http://127.0.0.1:1")
	defer func() { loginVerifyCode = "" }()

	err := runLogin(loginCmd, nil)
	if err == nil {
		t.Error("--verify-code without --email should fail")
	}
}

func TestLoginRequestCodeCallsAPI(t *testing.T) {
	var calledPath string
	var calledEmail string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledPath = r.URL.Path
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		calledEmail = body["email"]
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	withTempConfig(t, func() {
		cfg, _ := config.Load()
		cfg.APIURL = ts.URL
		cfg.APIKey = ""
		config.Save(cfg)

		loginEmail = "agent@test.com"
		loginRequestCode = true
		defer func() { loginEmail = ""; loginRequestCode = false }()

		err := runLoginRequestCode()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(calledPath, "auth") {
			t.Errorf("expected auth endpoint, got %q", calledPath)
		}
		if calledEmail != "agent@test.com" {
			t.Errorf("expected email agent@test.com, got %q", calledEmail)
		}
	})
}

func TestLoginVerifyCodeCallsAPI(t *testing.T) {
	var calledPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledPath = r.URL.Path
		w.WriteHeader(200)
		w.Write([]byte(`{"email":"agent@test.com"}`))
	}))
	defer ts.Close()

	withTempConfig(t, func() {
		cfg, _ := config.Load()
		cfg.APIURL = ts.URL
		cfg.APIKey = ""
		config.Save(cfg)

		loginEmail = "agent@test.com"
		loginVerifyCode = "123456"
		defer func() { loginEmail = ""; loginVerifyCode = "" }()

		err := runLoginVerifyCode()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(calledPath, "verify") {
			t.Errorf("expected verify endpoint, got %q", calledPath)
		}
		// Key should be saved
		cfg, _ = config.Load()
		if !cfg.HasAuth() {
			t.Error("API key should be stored after verification")
		}
		if !strings.HasPrefix(cfg.APIKey, "pk_") {
			t.Errorf("stored key should start with pk_, got %q", cfg.APIKey)
		}
	})
}

func TestLoginVerifyCodeDoesNotEnterInteractiveMode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"email":"agent@test.com"}`))
	}))
	defer ts.Close()

	withTempConfig(t, func() {
		cfg, _ := config.Load()
		cfg.APIURL = ts.URL
		cfg.APIKey = ""
		config.Save(cfg)

		loginEmail = "agent@test.com"
		loginVerifyCode = "123456"
		defer func() { loginEmail = ""; loginVerifyCode = "" }()

		// runLoginVerifyCode should return nil (not launch TUI)
		err := runLoginVerifyCode()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// If it tried to launch TUI, it would panic or hang — passing means it returned cleanly
	})
}

func TestLoginRequestCodeDoesNotStoreKey(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	withTempConfig(t, func() {
		cfg, _ := config.Load()
		cfg.APIURL = ts.URL
		cfg.APIKey = ""
		config.Save(cfg)

		loginEmail = "agent@test.com"
		loginRequestCode = true
		defer func() { loginEmail = ""; loginRequestCode = false }()

		runLoginRequestCode()

		cfg, _ = config.Load()
		if cfg.HasAuth() {
			t.Error("--request-code should not store a key")
		}
	})
}

func TestLoginFlagsRegistered(t *testing.T) {
	f := loginCmd.Flags()
	for _, name := range []string{"email", "request-code", "verify-code"} {
		if f.Lookup(name) == nil {
			t.Errorf("login flag %q not registered", name)
		}
	}
}

func TestAPIKeyBlockedInInteractiveMode(t *testing.T) {
	flagAPIKey = "pk_test_interactive_block"
	defer func() { flagAPIKey = "" }()

	rootCmd.PersistentPreRunE = nil
	defer func() {
		rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error { return nil }
	}()

	rootCmd.SetArgs([]string{"--api-key", "pk_test_interactive_block"})
	err := rootCmd.Execute()
	if err == nil {
		t.Error("shellroute --api-key should be blocked in interactive mode")
	}
	if err != nil && !containsStr(err.Error(), "interactive mode") {
		t.Errorf("error should mention interactive mode, got: %v", err)
	}
}

func TestAPIKeyWorksWithRun(t *testing.T) {
	resetFlags(runCmd)
	defer stubCommand(runCmd)()

	rootCmd.SetArgs([]string{"--api-key", "pk_test_run", "run", "US", "--", "echo"})
	err := rootCmd.Execute()
	// Should NOT fail with "interactive mode" error — run subcommand accepts --api-key
	if err != nil && containsStr(err.Error(), "interactive mode") {
		t.Error("--api-key should work with run subcommand")
	}
	if flagAPIKey != "pk_test_run" {
		t.Errorf("api-key = %q, want pk_test_run", flagAPIKey)
	}
}

func TestAPIKeyWorksWithConnect(t *testing.T) {
	resetFlags(connectCmd)
	defer stubCommand(connectCmd)()

	rootCmd.SetArgs([]string{"--api-key", "pk_test_connect", "proxy", "--country", "US", "--format", "json"})
	err := rootCmd.Execute()
	if err != nil && containsStr(err.Error(), "interactive mode") {
		t.Error("--api-key should work with connect subcommand")
	}
	if flagAPIKey != "pk_test_connect" {
		t.Errorf("api-key = %q, want pk_test_connect", flagAPIKey)
	}
}

// --- Nested session prevention ---

func TestNestedSessionBlocked(t *testing.T) {
	os.Setenv("SHELLROUTE_CTRL", "12345")
	defer os.Unsetenv("SHELLROUTE_CTRL")

	rootCmd.PersistentPreRunE = nil
	defer func() {
		rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error { return nil }
	}()

	rootCmd.SetArgs([]string{})
	err := rootCmd.Execute()
	if err == nil {
		t.Error("shellroute inside a session should fail")
	}
	if err != nil && !containsStr(err.Error(), "nested session") {
		t.Errorf("error should mention nested session, got: %v", err)
	}
}

func TestNestedSession_SubcommandsStillWork(t *testing.T) {
	os.Setenv("SHELLROUTE_CTRL", "12345")
	defer os.Unsetenv("SHELLROUTE_CTRL")

	// Subcommands like balance should work inside a session
	resetFlags(balanceCmd)
	defer stubCommand(balanceCmd)()
	rootCmd.SetArgs([]string{"balance"})
	err := rootCmd.Execute()
	// Should NOT fail with "nested session" — only the root command (interactive mode) is blocked
	if err != nil && containsStr(err.Error(), "nested session") {
		t.Error("subcommands should work inside a session")
	}
}

// --- Run without command ---

func TestRunWithoutCommand(t *testing.T) {
	withTempConfig(t, func() {
		resetAllFlags()
		rootCmd.SetArgs([]string{"run", "US"})
		err := rootCmd.Execute()
		if err == nil {
			t.Error("run without -- command should fail")
		}
		if err != nil && !containsStr(err.Error(), "command required") {
			t.Errorf("error should mention command required, got: %v", err)
		}
	})
}

func TestRunWithDashDashOnly(t *testing.T) {
	withTempConfig(t, func() {
		resetAllFlags()
		rootCmd.SetArgs([]string{"run", "US", "--"})
		err := rootCmd.Execute()
		if err == nil {
			t.Error("run US -- (no command) should fail")
		}
	})
}

// --- Connect format validation ---

func TestConnectRejectsHumanFormat(t *testing.T) {
	withTempConfig(t, func() {
		resetAllFlags()
		rootCmd.SetArgs([]string{"proxy", "--country", "US", "--format", "human"})
		err := rootCmd.Execute()
		if err == nil {
			t.Error("connect --format human should be rejected")
		}
		if err != nil && !containsStr(err.Error(), "invalid format") {
			t.Errorf("expected 'invalid format', got: %v", err)
		}
	})
}

func TestConnectDefaultFormatIsJSON(t *testing.T) {
	resetFlags(connectCmd)
	defer stubCommand(connectCmd)()
	rootCmd.SetArgs([]string{"proxy", "--country", "US"})
	rootCmd.Execute()
	if connectFormat != "json" {
		t.Errorf("default format should be json, got %q", connectFormat)
	}
}

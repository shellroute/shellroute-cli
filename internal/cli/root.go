package cli

import (
	"fmt"
	"os"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/shellroute/shellroute-cli/internal/tui"
	"github.com/spf13/cobra"
)

var (
	flagAPIKey           string
	flagSkipVersionCheck bool
)

var rootCmd = &cobra.Command{
	Use:           "shellroute",
	Short:         "Residential proxy access from your terminal",
	Long:          "ShellRoute — pay-per-use residential proxy access from your terminal.",
	SilenceUsage:  true,
	SilenceErrors: true,
	CompletionOptions: cobra.CompletionOptions{
		HiddenDefaultCmd: true,
	},
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip version check for commands that don't need it
		name := cmd.Name()
		if name == "version" || name == "help" || name == "completion" {
			return nil
		}
		cfg, _ := loadConfig()
		if cfg != nil && !checkVersion(cfg) {
			return fmt.Errorf("version too old")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// Prevent nesting — already inside a shellroute session
		if os.Getenv("SHELLROUTE_CTRL") != "" {
			display.Error("Already inside a shellroute session. Use /help for commands.")
			return fmt.Errorf("nested session")
		}
		// --api-key is for direct mode only (run, connect, etc.), not interactive shell
		if flagAPIKey != "" {
			display.Error("--api-key is for direct commands (e.g. shellroute run --api-key ... US -- cmd).\n  For interactive mode, run `shellroute login`.")
			return fmt.Errorf("api-key not supported in interactive mode")
		}
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if !cfg.HasAuth() {
			display.Error("Not authenticated. Run `shellroute login`.")
			return fmt.Errorf("not authenticated")
		}
		tui.Version = Version()
		return tui.Run(cfg)
	},
}

func init() {
	v := Version()
	api.Version = v
	rootCmd.Version = v
	rootCmd.SetVersionTemplate("shellroute {{.Version}}\n")
	rootCmd.PersistentFlags().StringVar(&flagAPIKey, "api-key", "", "API key (overrides config and env var)")
	rootCmd.PersistentFlags().BoolVar(&flagSkipVersionCheck, "skip-version-check", false, "Skip version check (for CI)")

	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(connectCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(balanceCmd)
	rootCmd.AddCommand(countriesCmd)
	rootCmd.AddCommand(citiesCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(revealKeyCmd)
}

// loadConfig loads config and applies the --api-key flag override.
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	cfg.ApplyFlag(flagAPIKey)
	return cfg, nil
}

func Execute() error {
	err := rootCmd.Execute()
	if err != nil {
		// Cobra parse errors (unknown flag, bad args) are silenced by
		// SilenceErrors. Print them so the user sees what went wrong.
		// Our own RunE errors are already displayed via display.Error.
		// Cobra parse errors don't go through RunE, so they never get displayed.
		if !isHandledError(err) {
			display.Error("%s", err)
		}
	}
	return err
}

// isHandledError returns true if the error was already printed by a command handler.
func isHandledError(err error) bool {
	msg := err.Error()
	// These are returned by our RunE handlers after calling display.Error
	switch msg {
	case "not authenticated", "email required", "code required",
		"country required", "nested session",
		"api-key not supported in interactive mode",
		"version too old", "proxy already running":
		return true
	}
	return false
}

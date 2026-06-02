package cli

import (
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove stored credentials",
	RunE:  runLogout,
}

func runLogout(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		display.Error("Failed to load config.")
		return err
	}

	if !cfg.HasAuth() {
		display.Info("Not logged in.")
		return nil
	}

	cfg.APIKey = ""
	if err := config.Save(cfg); err != nil {
		display.Error("Failed to save config.")
		return err
	}

	display.Success("Logged out. Credentials removed.")
	return nil
}

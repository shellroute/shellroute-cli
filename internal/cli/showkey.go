package cli

import (
	"fmt"

	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/spf13/cobra"
)

var revealKeyCmd = &cobra.Command{
	Use:   "reveal-key",
	Short: "Print your API key (stored in ~/.shellroute/config.toml)",
	RunE:  runRevealKey,
}

func runRevealKey(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if !cfg.HasAuth() {
		display.Error("Not authenticated. Run `shellroute login` or use `--api-key`.")
		return fmt.Errorf("not authenticated")
	}

	fmt.Println(cfg.APIKey)
	return nil
}

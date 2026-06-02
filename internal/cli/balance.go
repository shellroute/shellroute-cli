package cli

import (
	"encoding/json"
	"fmt"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/spf13/cobra"
)

var balanceFormat string

var balanceCmd = &cobra.Command{
	Use:   "balance",
	Short: "Show remaining credit balance",
	RunE:  runBalance,
}

func init() {
	balanceCmd.Flags().StringVar(&balanceFormat, "format", "human", "Output format: human, json")
}

func runBalance(cmd *cobra.Command, args []string) error {
	if err := config.ValidateFormat(balanceFormat, "human", "json"); err != nil {
		display.Error("%s", err)
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if !cfg.HasAuth() {
		display.Error("Not authenticated. Run `shellroute login` or use `--api-key`.")
		return fmt.Errorf("not authenticated")
	}

	client := api.New(cfg.APIURL, cfg.APIKey)
	bal, err := client.GetBalance()
	if err != nil {
		display.Error("Cannot fetch balance. Check your connection.")
		return err
	}

	if balanceFormat == "json" {
		data, _ := json.MarshalIndent(bal, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	display.Label("Account", bal.Email)
	display.Label("Balance", display.FormatBalance(bal.BalanceUSD))
	display.Label("Rate", fmt.Sprintf("$%.2f/GB (residential) | $%.2f/GB (datacenter)",
		bal.Rates.ResidentialPerGB, bal.Rates.DatacenterPerGB))
	return nil
}

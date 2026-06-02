package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/spf13/cobra"
)

var statusFormat string

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show active connections and current IP",
	RunE:  runStatus,
}

func init() {
	statusCmd.Flags().StringVar(&statusFormat, "format", "human", "Output format: human, json")
}

func runStatus(cmd *cobra.Command, args []string) error {
	if err := config.ValidateFormat(statusFormat, "human", "json"); err != nil {
		display.Error("%s", err)
		return err
	}
	if ctrlPort := os.Getenv("SHELLROUTE_CTRL"); ctrlPort != "" {
		return runStatusInSession(ctrlPort)
	}

	cfg, _ := loadConfig()
	if cfg == nil || !cfg.HasAuth() {
		display.Error("Not authenticated. Run `shellroute login` or use `--api-key`.")
		return fmt.Errorf("not authenticated")
	}

	client := api.New(cfg.APIURL, cfg.APIKey)

	sessions, err := client.GetSessions()
	if err != nil {
		display.Error("Cannot fetch sessions. Check your connection.")
		return err
	}

	var active []api.SessionInfo
	for _, s := range sessions {
		if s.Status == "active" {
			active = append(active, s)
		}
	}

	bal, _ := client.GetBalance()

	if statusFormat == "json" {
		out := map[string]interface{}{
			"sessions": active,
			"balance":  nil,
		}
		if bal != nil {
			out["balance"] = map[string]interface{}{
				"usd":         bal.BalanceUSD,
				"low_balance": bal.LowBalance,
			}
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(active) == 0 {
		display.Info("No active sessions.")
		if bal != nil {
			display.Label("Balance", display.FormatBalance(bal.BalanceUSD))
		}
		return nil
	}

	fmt.Fprintln(cmd.ErrOrStderr(), display.Bold(fmt.Sprintf("Active sessions (%d):", len(active))))
	for _, s := range active {
		startTime, _ := time.Parse(time.RFC3339, s.StartedAt)
		uptime := display.FormatDuration(int(time.Since(startTime).Seconds()))
		data := display.FormatBytes(s.BytesUp + s.BytesDown)
		cost := display.FormatUSD(s.CostUSD)
		fmt.Fprintf(cmd.ErrOrStderr(), "  %s  %-4s  %s  %s  up %s\n",
			s.ID[:8], s.Country, data, cost, uptime)
	}

	if bal != nil {
		display.Label("Balance", display.FormatBalance(bal.BalanceUSD))
	}
	return nil
}

func runStatusInSession(ctrlPort string) error {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/status", ctrlPort))
	if err != nil {
		display.Error("Cannot reach session.")
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Print(strings.TrimSpace(string(body)))
	fmt.Println()
	return nil
}

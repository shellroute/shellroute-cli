package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/shellroute/shellroute-cli/internal/session"
	"github.com/spf13/cobra"
)

var (
	connectCountry  string
	connectCity     string
	connectType     string
	connectPort     int
	connectProtocol string
	connectFormat   string
	connectSticky   bool
)

var connectCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Start a persistent local proxy",
	RunE:  runConnect,
}

var proxyStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop all running proxy sessions",
	RunE:  runProxyStop,
}

func init() {
	f := connectCmd.Flags()
	f.StringVar(&connectCountry, "country", "", "ISO country code (e.g., US, DE, JP)")
	f.StringVar(&connectCity, "city", "", "City name (best-effort)")
	f.StringVar(&connectType, "iptype", "", "IP type: residential (default), datacenter, or mix")
	f.StringVar(&connectFormat, "format", "json", "Output format: json, env")
	f.BoolVar(&connectSticky, "sticky", false, "Keep same IP on reconnect")
	connectCmd.AddCommand(proxyStopCmd)
}

func runConnect(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if !cfg.HasAuth() {
		display.Error("Not authenticated. Run `shellroute login` or use `--api-key`.")
		return fmt.Errorf("not authenticated")
	}

	// Validate format (json or env only — interactive mode is via `shellroute` + `/connect`)
	if err := config.ValidateFormat(connectFormat, "json", "env"); err != nil {
		display.Error("%s", err)
		return err
	}

	// Block if another proxy session is already running
	if existing, _ := session.LoadActiveSessions(); len(existing) > 0 {
		var proxySessions []session.Info
		for _, s := range existing {
			if s.Mode == "proxy" {
				proxySessions = append(proxySessions, s)
			}
		}
		if len(proxySessions) > 0 {
			display.Error("Another proxy session is already running:")
			for _, s := range proxySessions {
				dur := sessionDuration(s.StartTime)
				if s.ExitIP != "" {
					display.Error("  PID %d · %s · %s · port %d · %s", s.PID, s.Country, s.ExitIP, s.Port, dur)
				} else {
					display.Error("  PID %d · %s · port %d · %s", s.PID, s.Country, s.Port, dur)
				}
			}
			display.Error("Run `shellroute proxy stop` first.")
			return fmt.Errorf("proxy already running")
		}
	}

	// Apply defaults
	country := connectCountry
	if country == "" {
		country = cfg.DefaultCountry
	}
	if country == "" {
		display.Error("Country required. Use --country US or set default_country in config.")
		return fmt.Errorf("country required")
	}
	country = config.NormalizeCountry(country)

	if err := config.ValidateCountry(country); err != nil {
		display.Error("%s", err)
		return err
	}
	if err := config.ValidateIPType(connectType); err != nil {
		display.Error("%s", err)
		return err
	}
	if err := config.ValidateCity(connectCity); err != nil {
		display.Error("%s", err)
		return err
	}

	return runConnectHeadless(cfg, country)
}

func runConnectHeadless(cfg *config.Config, country string) error {
	ipType := connectType
	if ipType == "" {
		ipType = cfg.DefaultType
	}

	port := connectPort
	if port == 0 {
		port = cfg.DefaultPort
	}

	client := api.New(cfg.APIURL, cfg.APIKey)

	sess, err := session.Start(
		context.Background(),
		client,
		&api.SessionCreateRequest{
			Country: country,
			City:    connectCity,
			Type:    ipType,
			Sticky:  connectSticky,
		},
		port,
		session.StartOpts{Mode: "proxy"},
	)
	if err != nil {
		return handleSessionError(err)
	}

	if sess.GetExitIP() == "" {
		sess.Stop()
		display.Error("Connection failed — no working upstream. Try again.")
		return fmt.Errorf("no exit IP")
	}

	switch connectFormat {
	case "json":
		outputJSON(sess)
	case "env":
		outputEnv(sess)
	}

	return waitAndDisconnect(sess)
}

func waitAndDisconnect(sess *session.Session) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Fprintln(os.Stderr)

	resp, err := sess.Stop()
	if err != nil {
		display.Error("Failed to disconnect. Try again.")
		return err
	}

	display.SessionSummary("Disconnected.", resp.DurationSec, resp.BytesTotal, resp.CostUSD, resp.BalanceUSD)
	return nil
}

func outputJSON(sess *session.Session) {
	data := map[string]interface{}{
		"proxy_url": sess.ProxyURL(),
		"exit_ip":   sess.GetExitIP(),
		"country":   sess.Country,
		"city":      sess.City,
		"type":      sess.Type,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

func outputEnv(sess *session.Session) {
	fmt.Printf("export HTTP_PROXY=%s\n", sess.ProxyURL())
	fmt.Printf("export HTTPS_PROXY=%s\n", sess.ProxyURL())
}

func sessionDuration(startTime string) string {
	t, err := time.Parse(time.RFC3339, startTime)
	if err != nil {
		return "?"
	}
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func runProxyStop(cmd *cobra.Command, args []string) error {
	sessions, err := session.LoadActiveSessions()
	if err != nil {
		display.Error("Cannot read session files.")
		return err
	}
	// Only stop proxy/run sessions, not interactive
	var proxySessions []session.Info
	for _, s := range sessions {
		if s.Mode == "proxy" {
			proxySessions = append(proxySessions, s)
		}
	}
	if len(proxySessions) == 0 {
		display.Info("No running proxy sessions.")
		return nil
	}

	stopped := 0
	for _, s := range proxySessions {
		proc, err := os.FindProcess(s.PID)
		if err != nil {
			continue
		}
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			continue
		}
		dur := sessionDuration(s.StartTime)
		if s.ExitIP != "" {
			display.Info("Stopped PID %d · %s · %s · %s", s.PID, s.Country, s.ExitIP, dur)
		} else {
			display.Info("Stopped PID %d · %s · %s", s.PID, s.Country, dur)
		}
		stopped++
	}

	if stopped == 0 {
		display.Info("No sessions to stop.")
	}
	return nil
}

func handleSessionError(err error) error {
	apiErr, ok := err.(*api.APIError)
	if !ok {
		display.Error("Connection failed. Check your connection and try again.")
		return err
	}

	switch {
	case apiErr.IsAuthError():
		display.Error("Authentication failed. Run `shellroute login` to re-authenticate.")
	case apiErr.IsInsufficientBalance():
		display.Error("Insufficient balance. Add credits at https://console.shellroute.com")
	case apiErr.IsNoNodes():
		display.Error("No %s nodes available in %s. Try --type datacenter or a different country.",
			connectType, connectCountry)
	default:
		display.Error("%s", apiErr.UserMessage())
	}
	return err
}

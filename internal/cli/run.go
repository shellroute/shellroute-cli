//go:build !windows

package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/shellroute/shellroute-cli/internal/session"
	"github.com/spf13/cobra"
)

var (
	runCountry string
	runCity    string
	runIPType  string
	runPort    int
	runNoStat  bool
	runSticky  bool
)

var runCmd = &cobra.Command{
	Use:   "run [country] [flags] -- <command> [args...]",
	Short: "Run a command through the proxy",
	Long: `Creates a proxy session, runs the command with HTTP_PROXY/HTTPS_PROXY set, then tears down.

Examples:
  shellroute run US -- curl https://httpbin.org/ip
  shellroute run --country DE -- python scraper.py
  shellroute run GB -- wget https://example.com`,
	Args: cobra.MinimumNArgs(1),
	RunE: runRun,
}

func init() {
	f := runCmd.Flags()
	f.StringVar(&runCountry, "country", "", "ISO country code (e.g., US, DE, JP)")
	f.StringVar(&runCity, "city", "", "City name (best-effort)")
	f.StringVar(&runIPType, "iptype", "", "IP type: residential (default), datacenter, or mix")
	f.BoolVar(&runSticky, "sticky", false, "Keep same IP on reconnect")
	f.BoolVar(&runNoStat, "no-stat", false, "Suppress session summary (clean output)")
}

func runRun(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if !cfg.HasAuth() {
		display.Error("Not authenticated. Run `shellroute login` or use `--api-key`.")
		return fmt.Errorf("not authenticated")
	}

	country := runCountry
	// If first arg looks like a country code (2 letters, all alpha), use it
	if country == "" && len(args) >= 1 && len(args[0]) == 2 && isAlpha(args[0]) {
		country = args[0]
		args = args[1:]
	}
	if country == "" {
		country = cfg.DefaultCountry
	}
	if country == "" {
		display.Error("Country required. Use: shellroute run US -- <command>")
		return fmt.Errorf("country required")
	}
	if len(args) == 0 {
		display.Error("Command required after --. Use: shellroute run %s -- <command>", country)
		return fmt.Errorf("command required")
	}
	country = config.NormalizeCountry(country)

	if err := config.ValidateCountry(country); err != nil {
		display.Error("%s", err)
		return err
	}
	if err := config.ValidateIPType(runIPType); err != nil {
		display.Error("%s", err)
		return err
	}
	if err := config.ValidateCity(runCity); err != nil {
		display.Error("%s", err)
		return err
	}

	ipType := runIPType
	if ipType == "" {
		ipType = cfg.DefaultType
	}

	port := runPort
	if port == 0 {
		port = cfg.DefaultPort
	}

	client := api.New(cfg.APIURL, cfg.APIKey)

	// Start session
	sess, err := session.Start(
		context.Background(),
		client,
		&api.SessionCreateRequest{
			Country: country,
			City:    runCity,
			Type:    ipType,
			Sticky:  runSticky,
		},
		port,
		session.StartOpts{TrackRelays: true, Mode: "run"},
	)
	if err != nil {
		return handleSessionError(err)
	}

	if sess.GetExitIP() == "" {
		sess.Stop()
		display.Error("Connection failed — no working upstream. Try again.")
		return fmt.Errorf("no exit IP")
	}

	// detectExitIPRetry already verified the tunnel passes traffic (got an exit IP).
	// No need for a second route health check — it just adds a failure window
	// where the tunnel can die between the two checks, causing spurious failures.

	// Run the child command with proxy env vars
	childCmd := exec.Command(args[0], args[1:]...)
	childCmd.Stdin = os.Stdin
	childCmd.Stdout = os.Stdout
	childCmd.Stderr = os.Stderr
	childCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own process group
	childCmd.Env = append(os.Environ(),
		"HTTP_PROXY="+sess.ProxyURL(),
		"HTTPS_PROXY="+sess.ProxyURL(),
		"http_proxy="+sess.ProxyURL(),
		"https_proxy="+sess.ProxyURL(),
	)

	if err := childCmd.Start(); err != nil {
		sess.Stop()
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Wire upstream death → kill child process group
	var childRunning atomic.Bool
	childRunning.Store(true)
	killCancel := make(chan struct{})

	sess.OnUpstreamDead = func(reason string) {
		if !childRunning.Load() {
			return
		}
		sess.Proxy().SetRouteDead(true) // close active relay conns
		if childCmd.Process != nil {
			syscall.Kill(-childCmd.Process.Pid, syscall.SIGTERM)
			go func() {
				select {
				case <-time.After(5 * time.Second):
					if childRunning.Load() && childCmd.Process != nil {
						syscall.Kill(-childCmd.Process.Pid, syscall.SIGKILL)
					}
				case <-killCancel:
				}
			}()
		}
		fmt.Fprintln(os.Stderr, "\n  Connection lost during execution. Command killed.")
	}

	childErr := childCmd.Wait()
	childRunning.Store(false)
	close(killCancel)

	// Tear down session
	resp, stopErr := sess.Stop()
	if !runNoStat {
		if childErr != nil {
			display.Error("Command failed: %s", args[0])
		}
		if stopErr != nil {
			display.Error("Failed to end session. Try again.")
		} else {
			display.SessionSummary("shellroute session ended.", resp.DurationSec, resp.BytesTotal, resp.CostUSD, resp.BalanceUSD)
			if resp.BalanceUSD <= 0.001 {
				display.Warn("Balance depleted. Top up at https://console.shellroute.com, then reconnect.")
			}
		}
	}

	if childErr != nil {
		if exitErr, ok := childErr.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return childErr
	}
	return nil
}

func isAlpha(s string) bool {
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return len(s) > 0
}

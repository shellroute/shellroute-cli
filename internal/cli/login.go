package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/auth"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/shellroute/shellroute-cli/internal/tui"
	"github.com/spf13/cobra"
)

var (
	loginEmail       string
	loginRequestCode bool
	loginVerifyCode  string
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in with your email",
	Long: `Sends a one-time code to your email. Enter the code to authenticate.

Interactive (for humans):
  shellroute login

Direct (for agents and scripts):
  shellroute login --email user@example.com --request-code
  shellroute login --email user@example.com --verify-code 847291

After login, use shellroute reveal-key to get the stored API key.`,
	RunE: runLogin,
}

func init() {
	loginCmd.Flags().StringVar(&loginEmail, "email", "", "Email address (non-interactive mode)")
	loginCmd.Flags().BoolVar(&loginRequestCode, "request-code", false, "Request a verification code (non-interactive)")
	loginCmd.Flags().StringVar(&loginVerifyCode, "verify-code", "", "Verify with this code (non-interactive)")
}

func runLogin(cmd *cobra.Command, args []string) error {
	// Check if already logged in — applies to all modes
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.HasAuth() {
		client := api.New(cfg.APIURL, cfg.APIKey)
		if account, err := client.GetAccount(); err == nil {
			display.Info("Already logged in as %s", account.Email)
			display.Info("Run `shellroute logout` first to switch accounts.")
			return nil
		}
	}

	// Direct: --request-code
	if loginRequestCode {
		return runLoginRequestCode()
	}

	// Direct: --verify-code
	if loginVerifyCode != "" {
		return runLoginVerifyCode()
	}

	// Interactive mode
	return runLoginInteractive()
}

func runLoginRequestCode() error {
	if loginEmail == "" {
		display.Error("--email required with --request-code")
		return fmt.Errorf("email required")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	client := api.New(cfg.APIURL, "")
	if err := client.SendLoginCode(loginEmail); err != nil {
		if apiErr, ok := err.(*api.APIError); ok {
			display.Error("%s", apiErr.UserMessage())
			return err
		}
		display.Error("Cannot reach ShellRoute. Check your internet connection.")
		return err
	}

	display.Success("Code sent to %s", loginEmail)
	return nil
}

func runLoginVerifyCode() error {
	if loginEmail == "" {
		display.Error("--email required with --verify-code")
		return fmt.Errorf("email required")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	client := api.New(cfg.APIURL, "")

	// Generate API key locally — only the hash crosses the wire
	rawKey, err := auth.GenerateRawKey()
	if err != nil {
		display.Error("Failed to generate API key")
		return err
	}
	keyHash := auth.HashClientKey(rawKey)
	keyPrefix := auth.KeyPrefix(rawKey)

	_, err = client.VerifyLoginCode(loginEmail, loginVerifyCode, keyHash, keyPrefix)
	if err != nil {
		if apiErr, ok := err.(*api.APIError); ok {
			display.Error("%s", apiErr.UserMessage())
			return err
		}
		display.Error("Verification failed. Try again.")
		return err
	}

	cfg.APIKey = rawKey
	if err := config.Save(cfg); err != nil {
		display.Error("Failed to save credentials.")
		return err
	}

	display.Success("Logged in successfully")
	return nil
}

func runLoginInteractive() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("  Email: ")
	email, _ := reader.ReadString('\n')
	email = strings.TrimSpace(email)
	if email == "" {
		display.Error("Email required.")
		return fmt.Errorf("email required")
	}

	client := api.New(cfg.APIURL, "")
	if err := client.SendLoginCode(email); err != nil {
		if apiErr, ok := err.(*api.APIError); ok {
			display.Error("%s", apiErr.UserMessage())
			return err
		}
		display.Error("Cannot reach ShellRoute. Check your internet connection.")
		return err
	}

	fmt.Print("  Enter code sent to your email: ")
	code, _ := reader.ReadString('\n')
	code = strings.TrimSpace(code)
	if code == "" {
		display.Error("Code required.")
		return fmt.Errorf("code required")
	}

	// Generate API key locally — only the hash crosses the wire
	rawKey, err := auth.GenerateRawKey()
	if err != nil {
		display.Error("Failed to generate API key")
		return err
	}
	keyHash := auth.HashClientKey(rawKey)
	keyPrefix := auth.KeyPrefix(rawKey)

	_, err = client.VerifyLoginCode(email, code, keyHash, keyPrefix)
	if err != nil {
		if apiErr, ok := err.(*api.APIError); ok {
			display.Error("%s", apiErr.UserMessage())
			return err
		}
		display.Error("Verification failed. Try again.")
		return err
	}

	cfg.APIKey = rawKey
	if err := config.Save(cfg); err != nil {
		display.Error("Failed to save credentials.")
		return err
	}

	display.Success("Logged in successfully")

	// Enter interactive mode
	tui.Version = Version()
	return tui.Run(cfg)
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/shellroute/shellroute-cli/internal/proxy"
	"github.com/spf13/cobra"
)

var upstreamAddNote string

var upstreamCmd = &cobra.Command{
	Use:   "upstream",
	Short: "Use your own proxies",
	Long: `Save proxies you already have and route through them with no Shellroute account.

Save one under an alias you choose (your-proxy-name below), then use that
alias wherever a country code goes:
  shellroute upstream add your-proxy-name http://USER:PASSWORD@HOST:PORT
  shellroute run your-proxy-name -- curl https://ipinfo.io/ip
  shellroute proxy --upstream your-proxy-name
  shellroute                                   # then /connect your-proxy-name

Aliases are 3 to 32 characters: lowercase letters, digits, _ or -.

URL schemes: http, https (TLS to the proxy), socks5, socks5h. The port is
required. Username and password go in the URL exactly as your provider gave
them; wrap the URL in single quotes when they contain spaces or symbols. The
child command only ever sees the local proxy on 127.0.0.1; your proxy's
password never reaches it. Where the proxy exits is observed when a route
starts, not declared.`,
}

var upstreamAddCmd = &cobra.Command{
	Use:   "add <name> <url>",
	Short: "Save your own proxy under a name",
	Args:  exactArgsQuoted(2, "shellroute upstream add your-proxy-name 'scheme://user:password@host:port'"),
	RunE:  runUpstreamAdd,
}

var upstreamListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved upstreams (passwords hidden)",
	Args:  cobra.NoArgs,
	RunE:  runUpstreamList,
}

var upstreamRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Delete a saved upstream",
	Args:  cobra.ExactArgs(1),
	RunE:  runUpstreamRemove,
}

var upstreamTestCmd = &cobra.Command{
	Use:   "test <name|url>",
	Short: "Open one tunnel through a proxy and report its exit IP",
	Args:  exactArgsQuoted(1, "shellroute upstream test 'scheme://user:password@host:port'"),
	RunE:  runUpstreamTest,
}

// exactArgsQuoted is cobra.ExactArgs(n) that, given too many arguments,
// explains the likely cause: the shell split an unquoted URL.
func exactArgsQuoted(n int, example string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return fmt.Errorf("too many arguments. %s: %s", proxy.QuoteHint, example)
		}
		return cobra.ExactArgs(n)(cmd, args)
	}
}

func init() {
	upstreamAddCmd.Flags().StringVar(&upstreamAddNote, "note", "", "Free-text note")
	upstreamCmd.AddCommand(upstreamAddCmd, upstreamListCmd, upstreamRemoveCmd, upstreamTestCmd)
}

func runUpstreamAdd(cmd *cobra.Command, args []string) error {
	name, rawURL := args[0], args[1]
	if err := config.ValidateUpstreamName(name); err != nil {
		return err
	}
	if _, err := proxy.ParseUpstreamURL(rawURL); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	replaced := cfg.AddUpstream(config.Upstream{Name: name, URL: strings.TrimSpace(rawURL), Note: upstreamAddNote, Literal: true})
	if err := config.Save(cfg); err != nil {
		return err
	}
	verb := "Saved"
	if replaced {
		verb = "Updated"
	}
	display.Success("%s upstream %s: %s", verb, name, proxy.RedactURL(rawURL))
	return nil
}

func runUpstreamList(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Upstreams) == 0 {
		display.Info("No saved upstreams. Add one: shellroute upstream add <name> <url>")
		return nil
	}
	rows := make([][]string, 0, len(cfg.Upstreams))
	for _, u := range cfg.Upstreams {
		rows = append(rows, []string{u.Name, proxy.RedactURL(u.URL), u.Note})
	}
	headers := []string{"NAME", "URL", "NOTE"}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	out := cmd.OutOrStdout()
	line := func(cells []string) {
		parts := make([]string, len(cells))
		for i, c := range cells {
			parts[i] = fmt.Sprintf("%-*s", widths[i], c)
		}
		fmt.Fprintln(out, strings.TrimRight(strings.Join(parts, "  "), " "))
	}
	line(headers)
	for _, r := range rows {
		line(r)
	}
	return nil
}

func runUpstreamRemove(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if !cfg.RemoveUpstream(args[0]) {
		return fmt.Errorf("unknown upstream %q. List saved ones with `shellroute upstream list`", args[0])
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	display.Success("Removed upstream %s", args[0])
	return nil
}

func runUpstreamTest(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	t, err := resolveUpstreamRef(cfg, args[0])
	if err != nil {
		return err
	}
	res, err := proxy.Check(context.Background(), t.upstream)
	switch {
	case err == nil:
		where := ""
		if res.Country != "" {
			where = " · " + res.Country + " (observed)"
		}
		display.Success("via %s · exit IP %s%s · %d ms", t.describe(), res.ExitIP, where, res.Latency.Milliseconds())
		return nil
	case proxy.TunnelFailed(err):
		display.Error("Cannot connect through %s: %s", t.describe(), proxy.Explain(err))
		return errors.New("upstream unreachable")
	default:
		display.Warn("Tunnel open via %s, but the exit IP could not be read: %s", t.describe(), proxy.Cause(err))
		return nil
	}
}

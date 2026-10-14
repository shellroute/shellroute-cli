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

var (
	upstreamAddCountry string
	upstreamAddNote    string
)

var upstreamCmd = &cobra.Command{
	Use:   "upstream",
	Short: "Use your own proxies",
	Long: `Save proxies you already have and route through them with no Shellroute account.

Save one, then use its name wherever a country goes:
  shellroute upstream add work http://user:pass@proxy.example.com:8080 --country DE
  shellroute run work -- curl https://ipinfo.io/ip
  shellroute proxy --upstream work
  shellroute                                   # then /connect work

URL schemes: http, https (TLS to the proxy), socks5, socks5h. The port is
required. Credentials go in the URL, URL-encoded. The child command only ever
sees the local proxy on 127.0.0.1; your proxy's password never reaches it.`,
}

var upstreamAddCmd = &cobra.Command{
	Use:   "add <name> <url>",
	Short: "Save your own proxy under a name",
	Args:  cobra.ExactArgs(2),
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
	Args:  cobra.ExactArgs(1),
	RunE:  runUpstreamTest,
}

func init() {
	upstreamAddCmd.Flags().StringVar(&upstreamAddCountry, "country", "", "Country label shown in status (not verified)")
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
	country := ""
	if upstreamAddCountry != "" {
		country = config.NormalizeCountry(upstreamAddCountry)
		if err := config.ValidateCountry(country); err != nil {
			return err
		}
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	replaced := cfg.AddUpstream(config.Upstream{Name: name, URL: strings.TrimSpace(rawURL), Country: country, Note: upstreamAddNote})
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
		rows = append(rows, []string{u.Name, proxy.RedactURL(u.URL), u.Country, u.Note})
	}
	headers := []string{"NAME", "URL", "COUNTRY", "NOTE"}
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
		if t.label != "" {
			where = " (" + t.label + ")"
		}
		display.Success("via %s · exit IP %s%s · %d ms", t.describe(), res.ExitIP, where, res.Latency.Milliseconds())
		return nil
	case proxy.TunnelFailed(err):
		display.Error("Cannot connect through %s: %s", t.describe(), proxy.Cause(err))
		return errors.New("upstream unreachable")
	default:
		display.Warn("Tunnel open via %s, but the exit IP could not be read: %v", t.describe(), err)
		return nil
	}
}

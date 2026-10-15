package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
	"github.com/shellroute/shellroute-cli/internal/proxy"
	"github.com/shellroute/shellroute-cli/internal/session"
	"github.com/spf13/cobra"
)

var (
	runProxyURL     string // run --proxy <url>
	runNoCheck      bool   // run --no-check
	connectUpstream string // proxy --upstream <name|url>
	connectNoCheck  bool   // proxy --no-check
)

// envProxyVar names the environment variable that selects an own proxy for
// `shellroute run` when no country or upstream is given, for CI.
const envProxyVar = "SHELLROUTE_PROXY"

// runTarget is what `shellroute run` resolved from its arguments: a hosted
// country, or the user's own upstream.
type runTarget struct {
	country  string         // hosted route; "" for an own route
	upstream proxy.Upstream // own route
	name     string         // saved upstream name; "" for --proxy or the environment
}

func (t runTarget) own() bool { return t.upstream != nil }

// describe names the own route for messages: the saved name, or the redacted URL.
func (t runTarget) describe() string {
	if t.name != "" {
		return t.name
	}
	return t.upstream.Describe()
}

var errCountryRequired = errors.New("country required")

// isCountryWord reports whether s has the shape of a country code.
func isCountryWord(s string) bool {
	return len(s) == 2 && isAlpha(s)
}

// resolveRunTarget decides where `shellroute run` routes and returns the
// remaining command arguments.
//
//	--proxy <url>      own, ad hoc
//	--country CC       hosted (as before)
//	<CC> before --     hosted country, two letters
//	<name> before --   own, a saved upstream
//	nothing            SHELLROUTE_PROXY if set, else default_country
//
// dashIdx is cmd.ArgsLenAtDash(): the number of arguments before "--", or
// -1 when "--" was not given. Without "--", the first word is a target only
// when it is a country code or a saved upstream name, as before.
func resolveRunTarget(cfg *config.Config, args []string, dashIdx int, proxyFlag, countryFlag, envProxy string) (runTarget, []string, error) {
	first, rest := "", args
	switch {
	case dashIdx > 1:
		return runTarget{}, nil, errors.New("give one country or upstream name before --")
	case dashIdx == 1:
		first, rest = args[0], args[1:]
	case dashIdx == -1 && len(args) > 0:
		if _, saved := cfg.FindUpstream(args[0]); isCountryWord(args[0]) || saved {
			first, rest = args[0], args[1:]
		}
	}

	if proxyFlag != "" {
		if first != "" {
			return runTarget{}, nil, errors.New("--proxy cannot be combined with a country or upstream name")
		}
		up, err := proxy.ParseUpstreamURL(proxyFlag)
		if err != nil {
			return runTarget{}, nil, fmt.Errorf("--proxy: %v", err)
		}
		return runTarget{upstream: up}, rest, nil
	}

	if countryFlag != "" {
		if first != "" {
			return runTarget{}, nil, errors.New("--country cannot be combined with a country or upstream name before --")
		}
		return runTarget{country: countryFlag}, rest, nil
	}

	if first != "" {
		if isCountryWord(first) {
			return runTarget{country: first}, rest, nil
		}
		entry, ok := cfg.FindUpstream(first)
		if !ok {
			return runTarget{}, nil, fmt.Errorf("unknown upstream %q. Save it with `shellroute upstream add %s <url>` or list saved ones with `shellroute upstream list`", first, first)
		}
		up, err := proxy.ParseUpstreamURL(entry.URL)
		if err != nil {
			return runTarget{}, nil, fmt.Errorf("saved upstream %q has an invalid URL (%v). Remove it and add it again", first, err)
		}
		return runTarget{upstream: up, name: first}, rest, nil
	}

	if envProxy != "" {
		up, err := proxy.ParseUpstreamURL(envProxy)
		if err != nil {
			return runTarget{}, nil, fmt.Errorf("%s: %v", envProxyVar, err)
		}
		return runTarget{upstream: up}, rest, nil
	}

	if cfg.DefaultCountry != "" {
		return runTarget{country: cfg.DefaultCountry}, rest, nil
	}
	return runTarget{}, rest, errCountryRequired
}

// resolveUpstreamRef accepts a saved upstream name or a proxy URL.
func resolveUpstreamRef(cfg *config.Config, ref string) (runTarget, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return runTarget{}, errors.New("upstream required")
	}
	if entry, ok := cfg.FindUpstream(ref); ok {
		up, err := proxy.ParseUpstreamURL(entry.URL)
		if err != nil {
			return runTarget{}, fmt.Errorf("saved upstream %q has an invalid URL (%v). Remove it and add it again", ref, err)
		}
		return runTarget{upstream: up, name: ref}, nil
	}
	if strings.Contains(ref, "://") {
		up, err := proxy.ParseUpstreamURL(ref)
		if err != nil {
			return runTarget{}, err
		}
		return runTarget{upstream: up}, nil
	}
	return runTarget{}, fmt.Errorf("unknown upstream %q. Save it with `shellroute upstream add %s <url>` or give a proxy URL", ref, ref)
}

// ownRouteSelected reports whether cmd will route through the user's own
// proxy, so commands that need no account can skip the version check (an
// API call) and work with the API unreachable.
func ownRouteSelected(cmd *cobra.Command, args []string) bool {
	if cmd.Name() == "upstream" || (cmd.Parent() != nil && cmd.Parent().Name() == "upstream") {
		return true
	}
	switch cmd.Name() {
	case "run":
		cfg, _ := loadConfig()
		if cfg == nil {
			cfg = config.Default()
		}
		t, _, err := resolveRunTarget(cfg, args, cmd.ArgsLenAtDash(), runProxyURL, runCountry, os.Getenv(envProxyVar))
		return err == nil && t.own()
	case "proxy":
		return connectUpstream != ""
	}
	return false
}

// viaLine formats the own-route status line:
// via work · exit IP 1.2.3.4 · DE (observed) · 212 ms.
func viaLine(sess *session.Session) string {
	who := sess.UpstreamName
	if who == "" {
		who = sess.Via
	}
	parts := []string{"via " + who}
	if ip := sess.GetExitIP(); ip != "" {
		parts = append(parts, "exit IP "+ip)
		if sess.Country != "" {
			parts = append(parts, sess.Country+" (observed)")
		}
	}
	if sess.ConnectMs > 0 {
		parts = append(parts, fmt.Sprintf("%d ms", sess.ConnectMs))
	}
	return strings.Join(parts, " · ")
}

// reportOwnStartError explains why the user's own proxy could not be used.
func reportOwnStartError(t runTarget, err error) error {
	if proxy.TunnelFailed(err) {
		display.Error("Cannot connect through %s: %s", t.describe(), proxy.Cause(err))
	} else {
		display.Error("Cannot start the local proxy: %s", err)
	}
	return errors.New("upstream unreachable")
}

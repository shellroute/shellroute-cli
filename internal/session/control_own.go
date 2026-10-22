package session

import (
	"context"
	"fmt"
	"net/http"

	"github.com/shellroute/shellroute-cli/internal/proxy"
)

// httpConnectOwn handles /connect?upstream=<name>: route the shell through
// one of the user's saved proxies. No account, no API call, no billing.
func (c *Controller) httpConnectOwn(w http.ResponseWriter, name string) {
	entry, ok := c.cfg.FindUpstream(name)
	if !ok {
		w.WriteHeader(400)
		fmt.Fprintf(w, "ERROR Unknown upstream %q. Save it with: shellroute upstream add %s <url>\n", name, name)
		return
	}
	up, err := proxy.ParseSavedUpstreamURL(entry.URL, entry.Literal)
	if err != nil {
		w.WriteHeader(400)
		fmt.Fprintf(w, "ERROR Saved upstream %q has an invalid URL. Remove it and add it again.\n", name)
		return
	}

	c.mu.Lock()
	oldSess := c.sess
	c.mu.Unlock()

	// Start on a temp port; the old session keeps running until the new one works.
	sess, err := StartOwn(context.Background(), up, name, 0, StartOpts{})
	if err != nil {
		if proxy.TunnelFailed(err) {
			fmt.Fprintf(w, "ERROR Cannot connect through %s: %s\n", name, proxy.Explain(err))
		} else {
			fmt.Fprintln(w, "ERROR Cannot start the local proxy. Try again.")
		}
		return
	}

	if oldSess != nil {
		c.mu.Lock()
		c.sess = nil
		c.mu.Unlock()
		oldSess.Stop()
	}

	c.wireUpstreamCallback(sess)
	c.adopt(sess)
	c.writeConnectExports(w, sess)
	if sess.ProbeWarning != "" {
		// Filtered out by the shell before eval and shown as a warning.
		fmt.Fprintf(w, "PROBE_WARNING=%s\n", sess.ProbeWarning)
	}
}

// adopt makes sess the current session and resets per-session state.
func (c *Controller) adopt(sess *Session) {
	c.mu.Lock()
	c.sess = sess
	c.healthy = true
	c.notifiedDead = false
	c.deadReason = ""
	c.successCount = 0
	c.lowBalanceWarned = false
	c.accumDuration = 0
	c.accumBytes = 0
	c.accumCost = 0
	c.mu.Unlock()
}

// writeConnectExports writes the shell exports for a connected session: the
// proxy environment the child tools read, then the SHELLROUTE_* variables
// the prompt and slash commands use.
func (c *Controller) writeConnectExports(w http.ResponseWriter, sess *Session) {
	proxyURL := sess.ProxyURL()
	fmt.Fprintf(w, "export HTTP_PROXY=%s\n", proxyURL)
	fmt.Fprintf(w, "export HTTPS_PROXY=%s\n", proxyURL)
	fmt.Fprintf(w, "export http_proxy=%s\n", proxyURL)
	fmt.Fprintf(w, "export https_proxy=%s\n", proxyURL)
	// Union NO_PROXY + no_proxy + loopback, assign identical result to both.
	w.Write([]byte(NoProxyUnionScript + "\n"))
	fmt.Fprintln(w, `_sr_np=$(_sr_union_no_proxy); export NO_PROXY="$_sr_np"; export no_proxy="$_sr_np"; unset _sr_np`)
	// Enable Node.js built-in fetch/http proxy support (Node 24.0+ fetch,
	// 24.5+ http/https, backported to 22.21+; older versions ignore it).
	// Only set if user hasn't configured it; track ownership for cleanup.
	fmt.Fprintln(w, NodeProxyOwnershipScript)
	fmt.Fprintf(w, "export SHELLROUTE_SESSION_ID=%s\n", sess.ID)
	fmt.Fprintf(w, "export SHELLROUTE_COUNTRY=%s\n", sess.Country)
	fmt.Fprintf(w, "export SHELLROUTE_COUNTRY_NAME=%s\n", shellQuote(c.countryName(sess.Country)))
	fmt.Fprintf(w, "export SHELLROUTE_CITY=%s\n", shellQuote(sess.City))
	fmt.Fprintf(w, "export SHELLROUTE_EXIT_IP=%s\n", sess.GetExitIP())
	fmt.Fprintf(w, "export SHELLROUTE_PORT=%d\n", sess.Port)
	if sess.Own {
		fmt.Fprintf(w, "export SHELLROUTE_UPSTREAM=%s\n", sess.UpstreamName)
	} else {
		fmt.Fprintln(w, "unset SHELLROUTE_UPSTREAM")
	}
}

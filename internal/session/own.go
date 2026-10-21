package session

import (
	"context"
	"fmt"
	"time"

	"github.com/shellroute/shellroute-cli/internal/meter"
	"github.com/shellroute/shellroute-cli/internal/proxy"
)

// StartOwn starts the local proxy over the user's own upstream. No account,
// no API call, no billing. name is the saved upstream name ("" for an ad-hoc
// URL).
//
// Unless opts.SkipExitIP is set, the upstream is checked first: one tunnel
// attempt and one retry, each bounded by the dial timeout. A tunnel failure
// returns the *proxy.DialError; when the tunnel opened but no exit IP could be
// read, the session starts and ProbeWarning says why. The exit IP and the
// country the exit info service reports for it are recorded as observed.
func StartOwn(ctx context.Context, up proxy.Upstream, name string, port int, opts StartOpts) (*Session, error) {
	port, err := reservePort(port)
	if err != nil {
		return nil, err
	}

	m := meter.New()
	proxyServer := proxy.NewServer(&proxy.Config{
		ListenPort:       port,
		Upstream:         up,
		Meter:            m,
		ByteBudget:       -1,
		TrackRelays:      opts.TrackRelays,
		FailureThreshold: proxy.OwnFailureThreshold,
	})

	ctx, cancel := context.WithCancel(ctx)

	id := name
	if id == "" {
		id = "url"
	}
	s := &Session{
		ID:           "own-" + id,
		Mode:         opts.Mode,
		Own:          true,
		UpstreamName: name,
		Via:          up.Describe(),
		Port:         port,
		StartTime:    time.Now(),
		meter:        m,
		proxy:        proxyServer,
		cancel:       cancel,
	}
	s.wireProxy()

	if err := s.writeInfoFile(); err != nil {
		cancel()
		return nil, fmt.Errorf("write session file: %w", err)
	}

	go func() {
		if err := proxyServer.Start(ctx); err != nil {
			_ = err
			s.removeInfoFile()
		}
	}()

	if !opts.SkipExitIP {
		res, err := proxy.Check(ctx, up)
		switch {
		case err == nil:
			s.SetExitIP(res.ExitIP)
			s.Country = res.Country
			s.ConnectMs = int(res.Latency.Milliseconds())
			// Record what was observed so status and stop can show it.
			s.writeInfoFile()
		case proxy.TunnelFailed(err):
			cancel()
			s.removeInfoFile()
			return nil, err
		default:
			s.ProbeWarning = proxy.Cause(err)
		}
	}

	return s, nil
}

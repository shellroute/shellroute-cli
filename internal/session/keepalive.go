package session

import (
	"context"
	"time"

	"github.com/shellroute/shellroute-cli/internal/api"
)

// keepAliveInterval is how often a hosted session started by `shellroute
// proxy` or `shellroute run` tells the service it is still in use. The
// service ends a hosted session that goes a few minutes without this signal,
// whether or not traffic flows. The interactive shell sends the same signal
// from its balance monitor.
var keepAliveInterval = 60 * time.Second

// keepAlive sends the session's keep-alive every interval until ctx ends.
// A failed call is not retried early; the next tick sends another.
func keepAlive(ctx context.Context, client *api.Client, sessionID, ipType string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			_, _ = client.GetBalance(api.BalanceOpts{IPType: ipType, SessionID: sessionID})
		}
	}
}

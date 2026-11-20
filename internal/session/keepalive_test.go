package session

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/shellroute/shellroute-cli/internal/api"
)

// serviceWindow stands in for the few minutes after which the service ends a
// hosted session that sends no keep-alive, scaled down with the keep-alive
// interval so the tests run in about a second.
const serviceWindow = 250 * time.Millisecond

type keepAliveCall struct {
	at        time.Time
	sessionID string
	ipType    string
}

// fakeService answers the API calls a hosted session makes and records each
// keep-alive.
type fakeService struct {
	mu      sync.Mutex
	created time.Time
	calls   []keepAliveCall
	ended   bool
}

func startFakeService(t *testing.T) (*fakeService, *api.Client) {
	t.Helper()
	f := &fakeService{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sessions/create":
			f.created = time.Now()
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"session_id":"sess-1","gateway":{"endpoint":"127.0.0.1:1","token":"tok"},"exit_ip":"203.0.113.7","country":"US","type":"residential","byte_budget":1000000}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account/balance":
			if id := r.URL.Query().Get("session_id"); id != "" {
				f.calls = append(f.calls, keepAliveCall{at: time.Now(), sessionID: id, ipType: r.URL.Query().Get("type")})
			}
			io.WriteString(w, `{"balance_usd":5}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/sessions/sess-1":
			f.ended = true
			io.WriteString(w, `{"session_id":"sess-1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sessions/connect-report":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return f, api.New(srv.URL, "pk_test")
}

func (f *fakeService) keepAlives() []keepAliveCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]keepAliveCall(nil), f.calls...)
}

func (f *fakeService) sessionEnded() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ended
}

// longestSilence is the longest stretch, from session creation until now,
// in which the service heard no keep-alive.
func (f *fakeService) longestSilence(now time.Time) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := append(append([]keepAliveCall(nil), f.calls...), keepAliveCall{at: now})
	last, longest := f.created, time.Duration(0)
	for _, c := range calls {
		if gap := c.at.Sub(last); gap > longest {
			longest = gap
		}
		last = c.at
	}
	return longest
}

func setKeepAliveInterval(t *testing.T, d time.Duration) {
	t.Helper()
	old := keepAliveInterval
	keepAliveInterval = d
	t.Cleanup(func() { keepAliveInterval = old })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func startHosted(t *testing.T, client *api.Client, mode string) *Session {
	t.Helper()
	t.Setenv("SHELLROUTE_HOME", t.TempDir())
	sess, err := Start(context.Background(), client, &api.SessionCreateRequest{Country: "US", Type: "residential"}, 0, StartOpts{Mode: mode})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return sess
}

// A hosted session from `shellroute proxy` or `shellroute run` must outlive
// the service window: it sends its own keep-alive for as long as it runs,
// and stops when the session ends.
func TestStart_ProxyAndRunModesKeepSessionAlive(t *testing.T) {
	setKeepAliveInterval(t, 10*time.Millisecond)
	for _, mode := range []string{"proxy", "run"} {
		t.Run(mode, func(t *testing.T) {
			svc, client := startFakeService(t)
			sess := startHosted(t, client, mode)

			time.Sleep(4 * serviceWindow)
			if gap := svc.longestSilence(time.Now()); gap >= serviceWindow {
				t.Errorf("longest stretch without a keep-alive = %v, want under %v", gap, serviceWindow)
			}
			for _, c := range svc.keepAlives() {
				if c.sessionID != "sess-1" || c.ipType != "residential" {
					t.Fatalf("keep-alive for session %q type %q, want sess-1 residential", c.sessionID, c.ipType)
				}
			}

			if _, err := sess.Stop(); err != nil {
				t.Fatalf("Stop: %v", err)
			}
			if !svc.sessionEnded() {
				t.Error("Stop did not end the session")
			}
			time.Sleep(3 * keepAliveInterval) // let a call sent before Stop arrive
			after := len(svc.keepAlives())
			time.Sleep(10 * keepAliveInterval)
			if n := len(svc.keepAlives()); n != after {
				t.Errorf("%d keep-alives sent after Stop", n-after)
			}
		})
	}
}

// The interactive shell's controller sends the keep-alive from its balance
// monitor; its sessions must not send a second one.
func TestStart_InteractiveSessionLeavesKeepAliveToController(t *testing.T) {
	setKeepAliveInterval(t, 10*time.Millisecond)
	svc, client := startFakeService(t)
	sess := startHosted(t, client, "")
	defer sess.Stop()

	time.Sleep(10 * keepAliveInterval)
	if n := len(svc.keepAlives()); n != 0 {
		t.Errorf("interactive session sent %d keep-alives itself", n)
	}
}

func TestKeepAlive_StopsWhenContextEnds(t *testing.T) {
	svc, client := startFakeService(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		keepAlive(ctx, client, "sess-1", "datacenter", 5*time.Millisecond)
		close(done)
	}()

	waitFor(t, func() bool { return len(svc.keepAlives()) >= 2 })
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("keepAlive still running 1s after its context ended")
	}
	after := len(svc.keepAlives())
	time.Sleep(50 * time.Millisecond)
	if n := len(svc.keepAlives()); n != after {
		t.Errorf("%d keep-alives sent after the context ended", n-after)
	}
	if c := svc.keepAlives()[0]; c.ipType != "datacenter" {
		t.Errorf("keep-alive type = %q, want datacenter", c.ipType)
	}
}

// The service ends a session after a few minutes without a keep-alive; one
// minute leaves room for missed calls.
func TestKeepAliveInterval_LeavesRoomForMissedCalls(t *testing.T) {
	if keepAliveInterval > time.Minute {
		t.Errorf("keepAliveInterval = %v, want at most 1m", keepAliveInterval)
	}
}

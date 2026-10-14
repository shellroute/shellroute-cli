package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shellroute/shellroute-cli/internal/session"
)

func writeSessionFile(t *testing.T, dir string, info session.Info) {
	t.Helper()
	data, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(dir, "session-"+itoa(info.PID)+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	s := ""
	if n == 0 {
		return "0"
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestLocalOwnSessions_ListsOnlyOwnRoutesOfLiveProcesses(t *testing.T) {
	dir := withOwnConfig(t)
	now := time.Now().Format(time.RFC3339)
	// This process is alive: an own route and a hosted route.
	writeSessionFile(t, dir, session.Info{PID: os.Getpid(), SessionID: "own-work", Country: "DE", Port: 41900, StartTime: now, Mode: "proxy", Upstream: "work", Via: "http://u:***@proxy.example:8080"})
	hosted := session.Info{PID: os.Getpid(), SessionID: "sess", Country: "US", Port: 41901, StartTime: now}
	data, _ := json.Marshal(hosted)
	os.WriteFile(filepath.Join(dir, "session-hosted.json"), data, 0600)

	own := localOwnSessions()
	if len(own) != 1 {
		t.Fatalf("own sessions = %+v, want one", own)
	}
	if own[0].Upstream != "work" || own[0].Country != "DE" || own[0].Via != "http://u:***@proxy.example:8080" || own[0].Mode != "proxy" {
		t.Errorf("own[0] = %+v", own[0])
	}
}

func TestStatus_WithoutLoginShowsOwnSessions(t *testing.T) {
	dir := withOwnConfig(t)
	writeSessionFile(t, dir, session.Info{PID: os.Getpid(), SessionID: "own-work", Port: 41900, StartTime: time.Now().Format(time.RFC3339), Mode: "proxy", Upstream: "work", Via: "socks5://h:1080"})

	out, err := execCLI(t, "status", "--format", "json")
	if err != nil {
		t.Fatalf("status without login but with an own session: %v", err)
	}
	if !strings.Contains(out, `"upstream": "work"`) {
		t.Errorf("json lacks the own session:\n%s", out)
	}
	if _, err := execCLI(t, "status"); err != nil {
		t.Errorf("human status: %v", err)
	}
}

func TestStatus_WithoutLoginAndNoSessionsStillFails(t *testing.T) {
	withOwnConfig(t)
	if _, err := execCLI(t, "status"); err == nil || err.Error() != "not authenticated" {
		t.Errorf("err = %v", err)
	}
}

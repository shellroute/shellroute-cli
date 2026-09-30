package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// --- Session info file ---

// Without a usable config directory (no $HOME), the bookkeeping file is
// skipped rather than failing the session — CI containers rely on this.
func TestWriteInfoFile_SkipsWhenConfigDirUnavailable(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("SHELLROUTE_HOME", "")

	s := &Session{ID: "sess-test", Country: "US"}
	if err := s.writeInfoFile(); err != nil {
		t.Fatalf("writeInfoFile() = %v, want nil when config dir is unavailable", err)
	}
}

func TestWriteInfoFile_WritesWhenConfigDirAvailable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHELLROUTE_HOME", dir)

	s := &Session{ID: "sess-test", Country: "US"}
	if err := s.writeInfoFile(); err != nil {
		t.Fatalf("writeInfoFile() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, s.sessionFileName())); err != nil {
		t.Errorf("session file not written: %v", err)
	}
}

// --- ExitIP thread safety ---

func TestGetSetExitIP_ThreadSafe(t *testing.T) {
	s := &Session{}
	s.SetExitIP("1.1.1.1")

	var wg sync.WaitGroup
	// Concurrent writers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if n%2 == 0 {
				s.SetExitIP("2.2.2.2")
			} else {
				s.SetExitIP("3.3.3.3")
			}
		}(i)
	}
	// Concurrent readers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ip := s.GetExitIP()
			if ip == "" {
				t.Error("GetExitIP returned empty during concurrent access")
			}
		}()
	}
	wg.Wait()

	ip := s.GetExitIP()
	if ip != "2.2.2.2" && ip != "3.3.3.3" {
		t.Errorf("unexpected final IP: %q", ip)
	}
}

func TestGetExitIP_ReturnsSetValue(t *testing.T) {
	s := &Session{}
	if s.GetExitIP() != "" {
		t.Error("new session should have empty IP")
	}
	s.SetExitIP("10.0.0.1")
	if s.GetExitIP() != "10.0.0.1" {
		t.Errorf("got %q, want 10.0.0.1", s.GetExitIP())
	}
}

// --- IP detection endpoints ---

func TestIPDetectEndpoints_HasFallback(t *testing.T) {
	if len(ipDetectEndpoints) < 2 {
		t.Errorf("expected at least 2 IP detection endpoints, got %d", len(ipDetectEndpoints))
	}
	// First should be ipinfo.io (primary)
	if ipDetectEndpoints[0] != "https://ipinfo.io/ip" {
		t.Errorf("first endpoint should be ipinfo.io, got %q", ipDetectEndpoints[0])
	}
}

func TestGatewayNeedsTLS(t *testing.T) {
	tests := []struct {
		endpoint string
		want     bool
	}{
		{"localhost:8080", false},
		{"127.0.0.1:8080", false},
		{"::1:8080", false},
		{"gw.shellroute.com:8080", true},
		{"us.gw.shellroute.com:443", true},
		{"192.168.1.100:8080", true},
		{"10.0.0.1:8080", true},
	}

	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			got := gatewayNeedsTLS(tt.endpoint)
			if got != tt.want {
				t.Errorf("gatewayNeedsTLS(%q) = %v, want %v", tt.endpoint, got, tt.want)
			}
		})
	}
}

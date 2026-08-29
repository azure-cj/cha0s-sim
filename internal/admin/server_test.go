package admin

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"cha0s-sim/internal/config"
)

func TestNewServerConstructsWithoutStartingListener(t *testing.T) {
	store := &config.Store{}
	s := NewServer(0, store)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if s.Port != 0 {
		t.Errorf("Port = %d, want 0", s.Port)
	}
	if s.Store != store {
		t.Errorf("Store not set to passed store")
	}
	if s.httpServer != nil {
		t.Errorf("httpServer should be nil before Start()")
	}
}

func TestStartServesDashboardPageAndHealthz(t *testing.T) {
	s := NewServer(18089, nil)

	go func() {
		if err := s.Start(); err != nil {
			// Start returns errors like http.ErrServerClosed when the
			// connection is closed via Shutdown; that's expected here.
			_ = err
		}
	}()

	time.Sleep(200 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:18089/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / status = %d, want 200", resp.StatusCode)
	}
	body := make([]byte, 1024)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])
	for _, sub := range []string{"cha0s;sim Admin Dashboard", "Status: Running"} {
		if !strings.Contains(content, sub) {
			t.Errorf("dashboard body missing %q; got: %q", sub, content)
		}
	}

	resp2, err := http.Get("http://127.0.0.1:18089/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want 200", resp2.StatusCode)
	}
	body2 := make([]byte, 16)
	n2, _ := resp2.Body.Read(body2)
	if got := strings.TrimSpace(string(body2[:n2])); got != "OK" {
		t.Errorf("GET /healthz body = %q, want OK", got)
	}

	if err := s.Shutdown(); err != nil {
		t.Errorf("Shutdown() = %v, want nil", err)
	}
}

func TestShutdownOnNeverStartedServerReturnsNil(t *testing.T) {
	s := NewServer(0, nil)
	if err := s.Shutdown(); err != nil {
		t.Errorf("Shutdown() on never-started server = %v, want nil", err)
	}
}
